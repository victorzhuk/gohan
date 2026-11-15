package fixture

type fakeDB struct{}

func (fakeDB) Begin(any) (*fakeTx, error) { return &fakeTx{}, nil }

type fakeTx struct{}

func (t *fakeTx) Commit() error   { return nil }
func (t *fakeTx) Rollback() error { return nil }

type fakeFlow struct{}

func (fakeFlow) Invoke(any, string) (string, error) { return "", nil }

type fakeTool struct{}

func (fakeTool) Call(any, string) (string, error) { return "", nil }

func badFlowInvoke(flow fakeFlow, db fakeDB) {
	tx, err := db.Begin(nil)
	if err != nil {
		return
	}
	flow.Invoke(nil, "hello")
	_ = tx.Commit()
}

func badToolCall(tool fakeTool, db fakeDB) {
	tx, err := db.Begin(nil)
	if err != nil {
		return
	}
	tool.Call(nil, "hello")
	_ = tx.Rollback()
}
