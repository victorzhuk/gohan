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

func storeOnlyWork(tx *fakeTx) {}

func okFlowCommit(flow fakeFlow, db fakeDB) {
	tx, err := db.Begin(nil)
	if err != nil {
		return
	}
	storeOnlyWork(tx)
	if err := tx.Commit(); err != nil {
		return
	}
	flow.Invoke(nil, "hello")
}

func okToolCommit(tool fakeTool, db fakeDB) {
	tx, err := db.Begin(nil)
	if err != nil {
		return
	}
	if err := tx.Commit(); err != nil {
		return
	}
	tool.Call(nil, "hello")
}
