# StepFun source references (checked October 10, 2026)

The product plan uses the *first-party* StepFun international API, not an inferred third-party gateway API.

1. **Official Step 5 Preview model guide:** https://platform.stepfun.ai/docs/en/guides/models/step-5-preview
   - Model ID `step-5-preview`, 1M context, 64k max output, tool calls, streaming, JSON output, `reasoning_effort` (`low`/`medium`/`high`), text/image/video input.
2. **Official Chat Completions reference:** https://platform.stepfun.ai/docs/en/api-reference/chat/chat-completion-create
   - `POST https://api.stepfun.ai/v1/chat/completions`; chat messages, functions/tool calls, streaming chunks, usage fields and standard errors.
3. **Official tool calling reference:** https://platform.stepfun.ai/docs/en/api-reference/tool-call
   - Model selects application-supplied functions; up to 128 tool declarations. In particular, built-in `web_search` is **not available** for Step 5 Preview; app must integrate its own if required.
4. **Official pricing and rate limits:** https://platform.stepfun.ai/docs/en/guides/pricing/details
   - At verification, Step 5 Preview: $1.00/1M cache-miss input, **$0.10/1M cached input**, $2.70/1M output; check again before billing. Do not hard-code stale prices or use $0.05 third-party figure for first-party rate.
5. **Official platform:** https://platform.stepfun.ai/
6. **Official Step Code harness overview (reference only, not a RepoSurgeon dependency):** https://platform.stepfun.ai/step-code

**Clarification:** This package does not claim live API connectivity, model performance or GitHub PR status. Claude Code must verify the actual running environment as it implements the product.
