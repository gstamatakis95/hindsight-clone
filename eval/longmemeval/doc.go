// Package longmemeval loads the LongMemEval benchmark and drives it against memd: it retains each
// question's conversation history into a fresh bank, waits for the pending-operations barrier, asks
// the question through Recall or Reflect and hands the answer to the judge, reporting accuracy per
// question category.
package longmemeval
