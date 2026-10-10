# 804 — Extraction: fact text, dates, fact types (no CCR for who/where/why)

Status: accepted (WP-08)

## Context
The extraction schemas, which mirror Hindsight's, return `what / when / where / who / why`. `ExtractedFact`
has only `Text`. The card asks for a CCR if `ExtractedFact` needs who/where/why to match Hindsight's verbose
mode.

## Decision
- **No CCR.** Hindsight does not store the dimensions separately. It folds them into the fact text as
  `what | When: when | Involving: who | why` (`fact_extraction.py`, "Build combined fact text"); `where` is
  dropped. memd does the same. Empty, null and `N/A` dimensions are left out.
- **Dates.** As in Hindsight, only `fact_kind = "event"` facts take `occurred_start / occurred_end` and a
  `time_expression`. A missing end equals the start. Conversation facts carry no dates.
  - Dates are parsed as RFC 3339, `YYYY-MM-DDThh:mm[:ss]` or `YYYY-MM-DD` (UTC when no zone is given);
    anything else is unset.
  - A relative `time_expression` with no absolute start is resolved by timex against the item's mention time.
  - A timeless item drops every date.
- **Fact type by speaker.** The prompt states who is speaking. That covers the narrator rule and the item
  context naming the first-person speaker. The model labels each fact:
  - `"assistant"` maps to `experience` (decision 404) and `"world"` maps to world;
  - anything else falls back to `fact_kind`, then world.

  memd has no `agent_name` knob (Hindsight deprecated its override), so `ExtractData.AgentName` stays empty.
- **Verbatim** collapses to one fact per chunk. The text is the chunk; dates come from the first entry; the
  mentions and labels of every entry are merged.
- **Chunks** mode is one world fact per chunk, with no call.
- **Causes.** `caused_by` keeps only indexes of earlier, kept facts, at most two. They are remapped past
  dropped facts and ignored when `extract_causal_links` is false.
- **Free-form entities** off drops the extracted mentions. Caller hints and labels are kept.

## Alternatives
- A CCR adding `Who / Where / Why` to `ExtractedFact` and `memory_units`: Hindsight has no such columns, and
  recall works on text.

## Spec lines involved
SPEC "Engines (retain)" (`ExtractedFact`), "Feature scope" (extraction modes); Hindsight
`retain/fact_extraction.py`; decision 404.
