// Copyright Project Harbor Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//    http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package opentelekomcloud

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"

	commonhttp "github.com/goharbor/harbor/src/common/http"
	"github.com/goharbor/harbor/src/common/http/modifier"
	"github.com/goharbor/harbor/src/lib/log"
	adp "github.com/goharbor/harbor/src/pkg/reg/adapter"
	"github.com/goharbor/harbor/src/pkg/reg/adapter/native"
	"github.com/goharbor/harbor/src/pkg/reg/model"
	"github.com/goharbor/harbor/src/pkg/registry/auth/basic"
)

func init() {
	err := adp.RegisterFactory(model.RegistryTypeOpenTelekomCloud, new(factory))
	if err != nil {
		log.Errorf("failed to register factory for Open Telekom Cloud: %v", err)
		return
	}
	log.Infof("the factory of Open Telekom Cloud adapter was registered")
}

type factory struct {
}

// Create ...
func (f *factory) Create(r *model.Registry) (adp.Adapter, error) {
	return newAdapter(r)
}

// AdapterPattern ...
func (f *factory) AdapterPattern() *model.AdapterPattern {
	return nil
}

var (
	_ adp.Adapter          = (*adapter)(nil)
	_ adp.ArtifactRegistry = (*adapter)(nil)
)

// Adapter is for images replications between harbor and Open Telekom Cloud image repository(SWR)
type adapter struct {
	*native.Adapter
	registry *model.Registry
	client   *commonhttp.Client
	// original http client with no modifier,
	// opentelekomcloud's some api interface with basic authorization,
	// some with bearer token authorization.
	oriClient *http.Client
}

// Info gets info about Open Telekom Cloud SWR
func (a *adapter) Info() (*model.RegistryInfo, error) {
	registryInfo := model.RegistryInfo{
		Type:                   model.RegistryTypeOpenTelekomCloud,
		Description:            "Adapter for Open Telekom Cloud SWR",
		SupportedResourceTypes: []string{model.ResourceTypeImage},
		SupportedResourceFilters: []*model.FilterStyle{
			{
				Type:  model.FilterTypeName,
				Style: model.FilterStyleTypeText,
			},
			{
				Type:  model.FilterTypeTag,
				Style: model.FilterStyleTypeText,
			},
		},
		SupportedTriggers: []string{
			model.TriggerTypeManual,
			model.TriggerTypeScheduled,
		},
	}
	return &registryInfo, nil
}

// ListNamespaces lists namespaces from Open Telekom Cloud SWR with the provided query conditions.
func (a *adapter) ListNamespaces(query *model.NamespaceQuery) ([]*model.Namespace, error) {
	var namespaces []*model.Namespace

	urls := fmt.Sprintf("%s/dockyard/v2/visible/namespaces", a.registry.URL)

	r, err := http.NewRequest("GET", urls, nil)
	if err != nil {
		return namespaces, err
	}

	r.Header.Add("content-type", "application/json; charset=utf-8")

	resp, err := a.client.Do(r)
	if err != nil {
		return namespaces, err
	}

	defer func(Body io.ReadCloser) {
		_ = Body.Close()
	}(resp.Body)

	code := resp.StatusCode
	if code >= 300 || code < 200 {
		body, _ := io.ReadAll(resp.Body)
		return namespaces, fmt.Errorf("[%d][%s]", code, string(body))
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return namespaces, err
	}

	var namespacesData otcNamespaceList
	err = json.Unmarshal(body, &namespacesData)
	if err != nil {
		return namespaces, err
	}
	reg := fmt.Sprintf(".*%s.*", strings.Replace(query.Name, " ", "", -1))

	for _, namespaceData := range namespacesData.Namespace {
		namespace := model.Namespace{
			Name:     namespaceData.Name,
			Metadata: namespaceData.metadata(),
		}
		b, err := regexp.MatchString(reg, namespace.Name)
		if err != nil {
			return namespaces, nil
		}
		if b {
			namespaces = append(namespaces, &namespace)
		}
	}
	return namespaces, nil
}

// ConvertResourceMetadata convert resource metadata for Open Telekom Cloud SWR
func (a *adapter) ConvertResourceMetadata(resourceMetadata *model.ResourceMetadata, namespace *model.Namespace) (*model.ResourceMetadata, error) {
	metadata := &model.ResourceMetadata{
		Repository: resourceMetadata.Repository,
		Vtags:      resourceMetadata.Vtags,
	}
	return metadata, nil
}

// PrepareForPush prepare for push to Open Telekom Cloud SWR
func (a *adapter) PrepareForPush(resources []*model.Resource) error {
	namespaces := map[string]struct{}{}
	for _, resource := range resources {
		var namespace string
		paths := strings.Split(resource.Metadata.Repository.Name, "/")
		if len(paths) > 0 {
			namespace = paths[0]
		}
		ns, err := a.GetNamespace(namespace)
		if err != nil {
			return err
		}
		if ns != nil && ns.Name == namespace {
			continue
		}
		namespaces[namespace] = struct{}{}
	}

	url := fmt.Sprintf("%s/dockyard/v2/namespaces", a.registry.URL)

	for namespace := range namespaces {
		namespacebyte, err := json.Marshal(struct {
			Namespace string `json:"namespace"`
		}{
			Namespace: namespace,
		})
		if err != nil {
			return err
		}

		r, err := http.NewRequest("POST", url, strings.NewReader(string(namespacebyte)))
		if err != nil {
			return err
		}

		r.Header.Add("content-type", "application/json; charset=utf-8")

		resp, err := a.client.Do(r)
		if err != nil {
			return err
		}
		defer func(Body io.ReadCloser) {
			_ = Body.Close()
		}(resp.Body)

		code := resp.StatusCode
		if code >= 300 || code < 200 {
			body, _ := io.ReadAll(resp.Body)
			return fmt.Errorf("[%d][%s]", code, string(body))
		}

		log.Debugf("namespace %s created", namespace)
	}
	return nil
}

// GetNamespace gets a namespace from Open Telekom Cloud SWR
func (a *adapter) GetNamespace(namespaceStr string) (*model.Namespace, error) {
	var namespace = &model.Namespace{
		Name:     "",
		Metadata: make(map[string]interface{}),
	}

	urls := fmt.Sprintf("%s/dockyard/v2/namespaces/%s", a.registry.URL, namespaceStr)
	r, err := http.NewRequest("GET", urls, nil)
	if err != nil {
		return namespace, err
	}

	r.Header.Add("content-type", "application/json; charset=utf-8")

	resp, err := a.client.Do(r)
	if err != nil {
		return namespace, err
	}

	defer func(Body io.ReadCloser) {
		_ = Body.Close()
	}(resp.Body)

	code := resp.StatusCode
	if code >= 300 || code < 200 {
		body, _ := io.ReadAll(resp.Body)
		return namespace, fmt.Errorf("[%d][%s]", code, string(body))
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return namespace, err
	}

	var namespaceData otcNamespace
	err = json.Unmarshal(body, &namespaceData)
	if err != nil {
		return namespace, err
	}

	namespace.Name = namespaceData.Name
	namespace.Metadata = namespaceData.metadata()

	return namespace, nil
}

// HealthCheck check health for Open Telekom Cloud SWR
func (a *adapter) HealthCheck() (string, error) {
	return model.Healthy, nil
}

func newAdapter(registry *model.Registry) (adp.Adapter, error) {
	var (
		modifiers  = []modifier.Modifier{}
		authorizer modifier.Modifier
	)
	if registry.Credential != nil {
		authorizer = basic.NewAuthorizer(
			registry.Credential.AccessKey,
			registry.Credential.AccessSecret)
		modifiers = append(modifiers, authorizer)
	}

	transport := commonhttp.GetHTTPTransport(commonhttp.WithInsecure(registry.Insecure))
	return &adapter{
		Adapter:  native.NewAdapter(registry),
		registry: registry,
		client: commonhttp.NewClient(
			&http.Client{
				Transport: transport,
			},
			modifiers...,
		),
		oriClient: &http.Client{
			Transport: transport,
		},
	}, nil
}

type otcNamespaceList struct {
	Namespace []otcNamespace `json:"namespaces"`
}

type otcNamespace struct {
	ID           int64  `json:"id" orm:"column(id)"`
	Name         string `json:"name"`
	CreatorName  string `json:"creator_name,omitempty"`
	DomainPublic int    `json:"-"`
	Auth         int    `json:"auth"`
	DomainName   string `json:"-"`
	UserCount    int64  `json:"user_count"`
	ImageCount   int64  `json:"image_count"`
}

func (ns otcNamespace) metadata() map[string]interface{} {
	var metadata = make(map[string]interface{})
	metadata["id"] = ns.ID
	metadata["creator_name"] = ns.CreatorName
	metadata["domain_public"] = ns.DomainPublic
	metadata["auth"] = ns.Auth
	metadata["domain_name"] = ns.DomainName
	metadata["user_count"] = ns.UserCount
	metadata["image_count"] = ns.ImageCount

	return metadata
}
