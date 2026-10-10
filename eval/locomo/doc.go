// Package locomo loads the LoCoMo long-conversation benchmark and drives it against memd: it retains
// each multi-session conversation into a fresh bank, waits for the pending-operations barrier, asks
// the benchmark questions and hands the answers to the judge, reporting accuracy per category.
package locomo
