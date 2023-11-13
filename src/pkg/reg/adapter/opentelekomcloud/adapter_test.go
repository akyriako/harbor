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
	"testing"

	"github.com/stretchr/testify/assert"
	gock "gopkg.in/h2non/gock.v1"

	"github.com/goharbor/harbor/src/pkg/reg/model"
)

var (
	mockAccessKey    = "kkkkk"
	mockAccessSecret = "sssss"
	mockUrl          string
)

func getMockAdapter(t *testing.T) *adapter {
	otcRegistry := &model.Registry{
		ID:          1,
		Name:        "Open Telekom Cloud",
		Description: "Adapter for Open Telekom Cloud SWR",
		Type:        model.RegistryTypeOpenTelekomCloud,
		URL:         mockUrl,
		Credential:  &model.Credential{AccessKey: mockAccessKey, AccessSecret: mockAccessSecret},
		Insecure:    false,
		Status:      "",
	}

	otcAdapter, err := newAdapter(otcRegistry)
	if err != nil {
		t.Fatalf("Failed to call newAdapter(), reason=[%v]", err)
	}

	a := otcAdapter.(*adapter)
	gock.InterceptClient(a.client.GetClient())
	gock.InterceptClient(a.oriClient)

	return a
}

func TestAdapter_Info(t *testing.T) {
	a := getMockAdapter(t)

	info, err := a.Info()
	if err != nil {
		t.Error(err)
	}
	t.Log(info)
}

func TestAdapter_PrepareForPush(t *testing.T) {
	defer gock.Off()
	gock.Observe(gock.DumpRequest)

	mockRequest().Get("/dockyard/v2/namespaces/domain_repo_new").
		Reply(200).BodyString("{}")

	mockRequest().Post("/dockyard/v2/namespaces").BodyString(`{"namespace":"domain_repo_new"}`).
		Reply(200)

	a := getMockAdapter(t)

	repository := &model.Repository{
		Name:     "domain_repo_new",
		Metadata: make(map[string]interface{}),
	}
	resource := &model.Resource{}
	metadata := &model.ResourceMetadata{
		Repository: repository,
	}
	resource.Metadata = metadata
	err := a.PrepareForPush([]*model.Resource{resource})
	assert.NoError(t, err)
}

func TestAdapter_HealthCheck(t *testing.T) {
	defer gock.Off()
	gock.Observe(gock.DumpRequest)

	a := getMockAdapter(t)

	health, err := a.HealthCheck()
	if err != nil {
		t.Error(err)
	}
	t.Log(health)
}
