// Package analyzer holds the txcheck go/analysis analyzer for the function literal passed to store.DB.InTx. It
// enforces the lexical case of two rules:
//
//   - no model call inside a transaction: the literal must not call a method on an ai.LLM, ai.Embedder or
//     ai.Reranker (or any type that implements one) nor on the library types listed in modelTypes;
//   - sequence first: a call to Enqueue*, or QueueModelRefresh, must not come before the first tx.Seq call in
//     the same literal when both occur.
//
// A call is matched as InTx when its method is named InTx and its last argument is a function literal. Only code
// written inside the literal is checked; calls made by functions it calls are not followed.
package analyzer
