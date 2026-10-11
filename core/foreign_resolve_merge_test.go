package gohan

import (
	"strings"
	"testing"

	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/types"
)

func TestBuildForeignPreservesNative(t *testing.T) {
	t.Run("build.foreign-preserves-native", func(t *testing.T) {
		stack, err := Build([]Option{
			WithModels(foreignFlowModel{}),
			WithApprovalPolicySource(&roundtripPolicy{}),
			WithCredentialSource(&authCreds{}),
			WithNativeAgent(NativeSpec{Request: FlowRequest{Name: "native-flow"}, Profile: "foreign-test"}),
			WithForeignAgent(ForeignSpec{
				Request:    FlowRequest{Name: "chat"},
				Profile:    "foreign-test",
				NewRuntime: func() runtime.Runtime { return &constructionRT{} },
				Tools:      []types.Tool{&governedCountTool{}},
			}),
		}...)
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		for _, flow := range []string{"native-flow", "chat"} {
			if _, ok := stack.resolvedNative(flow); !ok {
				t.Errorf("resolvedNative(%q): ok = false, want the merged registry entry", flow)
			}
		}
		if _, ok := stack.resolvedForeign("chat"); !ok {
			t.Error("resolvedForeign(chat): ok = false, want the foreign factory")
		}
	})

	t.Run("build.foreign-native-name-clash", func(t *testing.T) {
		_, err := Build([]Option{
			WithModels(foreignFlowModel{}),
			WithNativeAgent(NativeSpec{Request: FlowRequest{Name: "chat"}, Profile: "foreign-test"}),
			WithForeignAgent(ForeignSpec{
				Request:    FlowRequest{Name: "chat"},
				Profile:    "foreign-test",
				NewRuntime: func() runtime.Runtime { return &constructionRT{} },
			}),
		}...)
		if err == nil {
			t.Fatal("Build succeeded, want the duplicate-name failure")
		}
		if !strings.Contains(err.Error(), "chat") {
			t.Errorf("error = %v, want the flow name", err)
		}
	})
}
