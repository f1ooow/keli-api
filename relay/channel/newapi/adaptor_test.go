package newapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
)

// New API 渠道注册链路：渠道类型 -> API 类型 -> 支持的端点。
// 上游 merge 最容易在这里静默断链（渠道类型号被占用、映射漏加），所以单独钉住。
func TestNewAPIChannelRegistration(t *testing.T) {
	apiType, ok := common.ChannelType2APIType(constant.ChannelTypeNewAPI)
	if !ok || apiType != constant.APITypeNewAPI {
		t.Fatalf("ChannelType2APIType(%d) = (%d, %v), want (%d, true)",
			constant.ChannelTypeNewAPI, apiType, ok, constant.APITypeNewAPI)
	}

	if !common.IsResponsesCompactAPIType(apiType) {
		t.Error("New API 渠道应支持 /v1/responses/compact")
	}

	endpoints := common.GetEndpointTypesByChannelType(constant.ChannelTypeNewAPI, "gpt-4o")
	want := []constant.EndpointType{
		constant.EndpointTypeOpenAI,
		constant.EndpointTypeOpenAIResponse,
		constant.EndpointTypeOpenAIResponseCompact,
		constant.EndpointTypeAnthropic,
		constant.EndpointTypeGemini,
		constant.EndpointTypeEmbeddings,
	}
	if len(endpoints) != len(want) {
		t.Fatalf("endpoints = %v, want %v", endpoints, want)
	}
	for i, e := range want {
		if endpoints[i] != e {
			t.Errorf("endpoints[%d] = %q, want %q", i, endpoints[i], e)
		}
	}
}

// 三种上游协议各自需要不同的鉴权头，透传时必须按 RelayFormat 分别设置。
func TestSetupRequestHeaderByRelayFormat(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cases := []struct {
		name        string
		relayFormat types.RelayFormat
		wantHeaders map[string]string
		absent      []string
	}{
		{
			name:        "openai",
			relayFormat: types.RelayFormatOpenAI,
			wantHeaders: map[string]string{"Authorization": "Bearer sk-upstream"},
			absent:      []string{"X-Api-Key", "X-Goog-Api-Key"},
		},
		{
			name:        "claude",
			relayFormat: types.RelayFormatClaude,
			wantHeaders: map[string]string{
				"Authorization":     "Bearer sk-upstream",
				"X-Api-Key":         "sk-upstream",
				"Anthropic-Version": "2023-06-01",
			},
		},
		{
			name:        "gemini",
			relayFormat: types.RelayFormatGemini,
			wantHeaders: map[string]string{
				"Authorization":  "Bearer sk-upstream",
				"X-Goog-Api-Key": "sk-upstream",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

			info := &relaycommon.RelayInfo{
				RelayFormat: tc.relayFormat,
				ChannelMeta: &relaycommon.ChannelMeta{
					ApiKey:      "sk-upstream",
					ChannelType: constant.ChannelTypeNewAPI,
				},
			}

			header := http.Header{}
			adaptor := &Adaptor{}
			if err := adaptor.SetupRequestHeader(c, &header, info); err != nil {
				t.Fatalf("SetupRequestHeader: %v", err)
			}

			for k, want := range tc.wantHeaders {
				if got := header.Get(k); got != want {
					t.Errorf("header %q = %q, want %q", k, got, want)
				}
			}
			for _, k := range tc.absent {
				if got := header.Get(k); got != "" {
					t.Errorf("header %q = %q, want empty", k, got)
				}
			}
		})
	}
}

// 上游实例的模型列表是动态拉取的，内置列表必须为空，否则会在渠道里凭空多出模型。
func TestModelListIsDynamic(t *testing.T) {
	adaptor := &Adaptor{}
	if len(adaptor.GetModelList()) != 0 {
		t.Errorf("GetModelList() = %v, want empty", adaptor.GetModelList())
	}
	if adaptor.GetChannelName() != ChannelName {
		t.Errorf("GetChannelName() = %q, want %q", adaptor.GetChannelName(), ChannelName)
	}
}
