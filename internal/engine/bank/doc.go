// Package bank holds bank-level tooling: exporting and creating banks from templates (TemplateCodec),
// selecting the directives that apply to a request (DirectiveSelector), bank stats and tag listings
// from indexed COUNT queries (StatsReader), rendering the exact prompt an operation would send with
// no LLM call (PromptPreviewer), and deleting a bank (BankDeleter).
//
// DeleteBank is two-phase: the api side tombstones the bank, cancels its pending jobs and enqueues
// delete_bank in one transaction; the delete_bank job then removes rows in committed batches, deletes
// the bank's blob prefix and finally the bank row.
package bank
