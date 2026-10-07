package exec_test

import (
	"testing"

	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/std/tool/exec"
)

func TestHostExecAcknowledged(t *testing.T) {
	spec := types.ToolSpec{Name: "run_cmd", Effect: types.SideEffect}
	unacknowledged := exec.New(spec, exec.Cmd{})
	if ack, ok := unacknowledged.(interface{ HostExecAcknowledged() bool }); !ok || ack.HostExecAcknowledged() {
		t.Fatalf("acknowledged = %v, %v; want false, true", ok, ack)
	}
	acknowledged := exec.New(spec, exec.Cmd{}, exec.AllowHostExec())
	ack, ok := acknowledged.(interface{ HostExecAcknowledged() bool })
	if !ok || !ack.HostExecAcknowledged() {
		t.Fatalf("acknowledged = %v, %v; want true, true", ok, ack)
	}
}
