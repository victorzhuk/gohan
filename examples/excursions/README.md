# excursions — S1 excursion demo

An offline walk through gohan's governed native path: a scripted model
plans a weekend trip through a native conversation with in-memory stores.
The flow registers as a `NativeSpec` with fixture-backed tool doubles and
its own decider. The decider allows the read-only trail lookup and denies
the side-effecting cabin booking, so the batch gate settles every call
before the first one executes and the denial lands in the persisted
history as a `not_executed: denied by policy` result.

Nothing reaches the network and no key is needed.

Run it:

```
go -C examples run ./excursions/
```

Test it:

```
go -C examples test -short -timeout 2m ./excursions/ -run 'TestExcursionsS1Offline' -v
```
