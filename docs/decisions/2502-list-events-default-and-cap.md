# 2502 — ListEvents: default 100, cap 1000

Status: accepted (WP-25)

`ListEventsRequest.limit` says "0 = server default". The server passed it straight through, and
`EventRepo.List` treats a limit of 0 or less as no limit, so `limit: 0` returned the bank's whole event
history. The RPC's reader (`operationReader.ListEvents` in cmd/memd, the engine-level glue for this RPC) now
turns 0 into 100 and caps any limit at 1000. Those are the bounds the memory and bank listings apply in
their engines. A negative limit is still INVALID_ARGUMENT at the server.

`EventRepo.List` keeps its port contract (`<= 0` = no limit) for internal callers.

Test: `memd.TestListEventsDefaultAndCap`.
