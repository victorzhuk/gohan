package fixture

func badDeferredRollback(flow fakeFlow, db fakeDB) {
	tx, err := db.Begin(nil)
	if err != nil {
		return
	}
	defer tx.Rollback()
	flow.Invoke(nil, "hello")
}
