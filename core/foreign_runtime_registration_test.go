package gohan

import (
	"strings"
	"testing"

	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/stores"
)

func TestForeignRuntimeRegistration(t *testing.T) {
	spec := func(name string) ForeignSpec {
		return ForeignSpec{
			Request:    FlowRequest{Name: name},
			Profile:    "foreign-test",
			NewRuntime: func() runtime.Runtime { return &constructionRT{} },
		}
	}
	base := func(extra ...Option) []Option {
		return append([]Option{
			WithModels(foreignFlowModel{}),
			WithForeignAgent(spec("chat")),
		}, extra...)
	}

	t.Run("foreign_runtime_registers_once", func(t *testing.T) {
		st, err := Build(base(WithStores(stores.Stores{}))...)
		if err != nil {
			t.Fatal(err)
		}
		newRT, ok := st.resolvedForeign("chat")
		if !ok {
			t.Fatal("foreign definition not registered")
		}
		if newRT == nil || newRT() == nil {
			t.Fatal("runtime factory missing or nil product")
		}
		if _, ok := st.resolvedNative("chat"); !ok {
			t.Fatal("foreign definition must resolve through the native pipeline")
		}
	})

	t.Run("duplicate_registration_refused", func(t *testing.T) {
		_, err := Build(base(
			WithStores(stores.Stores{}),
			WithForeignAgent(spec("chat")),
		)...)
		if err == nil || !strings.Contains(err.Error(), "already registered") {
			t.Fatalf("duplicate foreign registration err = %v, want already registered", err)
		}
		_, err = Build(append([]Option{
			WithModels(foreignFlowModel{}),
			WithStores(stores.Stores{}),
		}, WithNativeAgent(NativeSpec{Request: FlowRequest{Name: "chat"}, Profile: "foreign-test"}), WithForeignAgent(spec("chat")))...)
		if err == nil || !strings.Contains(err.Error(), "already registered") {
			t.Fatalf("native/foreign name clash err = %v, want already registered", err)
		}
		_, err = Build(base(WithStores(stores.Stores{}), WithForeignAgent(ForeignSpec{
			Request: FlowRequest{Name: "bare"},
			Profile: "foreign-test",
		}))...)
		if err == nil {
			t.Fatal("foreign definition without a runtime factory must fail the build")
		}
	})
}
