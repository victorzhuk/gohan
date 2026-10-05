# excursions — S1 excursion demo

An offline walk through gohan's flow, runtime and permission paths: a
scripted model plans a weekend trip on the native runtime with in-memory
stores. Its first turn is a two-call tool batch served by fixture-backed
tool doubles. The fixture permission policy allows the read-only trail
lookup and denies the side-effecting cabin booking, so the run settles
the batch gate-all-first and the denial shows up in the appended history
as a `not_executed: denied by policy` result.

Nothing reaches the network and no key is needed.

Run it:

```
go -C examples run ./excursions/
```

Test it:

```
go -C examples test -short -timeout 2m ./excursions/ -run 'TestExcursionsS1Offline' -v
```
