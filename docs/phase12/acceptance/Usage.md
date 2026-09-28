# Model usage verification

## Delivered scope

The application ledger records text calls independently of story commits. Input, output, reasoning and cache counters retain unknown values. DeepSeek Chat Completions and OpenAI Responses counters are supported. Connection checks and memory maintenance use the same ledger. Copies do not duplicate application-level usage.

The Model Usage panel supports account-wide and current-world views, weighted cache rates, counter coverage, request status, duration and paginated history. It contains no story text, private character records, credentials or reasoning content. Provider billing remains authoritative.

## Engineering verification — 2026-09-28

- Provider fixtures: reported and absent cache counters, output-limit errors and Responses usage parsing.
- Ledger: concurrent calls, failure metadata, restart, owner isolation, world authorization, unknown values and 102-record pagination.
- HTTP: read-only method and cursor validation.
- Relevant Go packages, frontend type check and production build passed.

## Browser verification

The production frontend and real local HTTP API were exercised with temporary worlds and an injected model. The account panel, empty state, unknown-usage calls, Escape closing and 390×844 layout were checked with gstack browse. The mobile document width remained 390 pixels; the call table scrolls inside its container. This is viewport verification, not physical-phone keyboard verification.

## Boundaries

No paid model calls were made for this feature's verification. Historical test usage cannot be reconstructed from this ledger. Real provider cache percentages will appear only after new requests report those fields. Application interruption may leave unconfirmed records; these are not zero-cost claims.
