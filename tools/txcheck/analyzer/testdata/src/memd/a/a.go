package a

import (
	"context"

	"memd/internal/ai"
	"memd/internal/recall"
	"store"
)

type myLLM struct{}

func (myLLM) Complete(context.Context) error { return nil }

func EnqueueThing(tx store.Tx)   {}
func QueueModelRefresh(store.Tx) {}

func good(ctx context.Context, db store.DB, llm ai.LLM) {
	_ = llm.Complete(ctx) // outside a transaction: fine
	_ = db.InTx(ctx, func(tx store.Tx) error {
		tx.Seq("b")
		tx.Queue().Enqueue(ctx)
		EnqueueThing(tx)
		return nil
	})
	_ = db.InTx(ctx, func(tx store.Tx) error {
		tx.Queue().Enqueue(ctx) // no Seq in the literal: not this rule's case
		return nil
	})
}

func models(ctx context.Context, db store.DB, llm ai.LLM, emb ai.Embedder, rr ai.Reranker, r *ai.Router,
	e *recall.Engine) {
	_ = db.InTx(ctx, func(tx store.Tx) error {
		_ = llm.Complete(ctx)     // want `no-model-call-in-tx`
		_ = emb.Embed(ctx)        // want `no-model-call-in-tx`
		_ = rr.Rerank(ctx)        // want `no-model-call-in-tx`
		_ = myLLM{}.Complete(ctx) // want `no-model-call-in-tx`
		r.Resolve()               // want `no-model-call-in-tx`
		e.Recall()                // want `no-model-call-in-tx`
		return nil
	})
}

func ordering(ctx context.Context, db store.DB) {
	_ = db.InTx(ctx, func(tx store.Tx) error {
		tx.Queue().Enqueue(ctx) // want `seq-before-enqueue`
		EnqueueThing(tx)        // want `seq-before-enqueue`
		QueueModelRefresh(tx)   // want `seq-before-enqueue`
		tx.Seq("b")
		return nil
	})
}
