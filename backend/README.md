# Backend

Go API and background workers for the content engine. See the [root
README](../README.md) for how to run the stack.

## Layout

```
cmd/server            entry point: migrate, seed, start workers, serve
internal/
  config              environment configuration
  database            connection, migrations, starter subject catalogue
  models              the schema
  extract             exam-agnostic question parser
  pattern             pattern inference from real papers
  converter           HTTP client for the Python conversion service
  pipeline            job workers: ingest, pattern analysis, paper build
  blueprint           paper assembly from a pattern
  handler             HTTP handlers
  server              router, CORS, optional API-key auth
```

## The schema

Taxonomy is shared, which is what allows content reuse:

- `Exam` — a target examination. No exams ship with the engine.
- `Subject` — a **global** catalogue entry with `aliases`: the headings this
  subject is printed under in real documents. The parser matches section headings
  against these, which is why recognising a new exam's layout means adding data
  rather than code.
- `Topic` — a chapter inside a subject, also with aliases.
- `ExamSubject` — joins the two, carrying each exam's own display label so one
  exam can print "Quantitative Aptitude" where another prints "Numerical
  Ability" while both point at the same `Subject`.

Pattern and cross-exam wiring:

- `ExamPattern` / `PatternSection` — versioned blueprints. `source` is `manual`
  or `derived`; derived patterns carry a `confidence` and `derived_from_count`.
- `ExamAssociation` — directed: `ExamID` borrows from `SourceExamID`, optionally
  scoped to one `SubjectID`, with a `Similarity` the generator prefers on.

Documents:

- `Document` — metadata. `ExamID` is nullable, so a document can be generic
  reference material.
- `DocumentBlob` — the file bytes, in a table of their own so listing documents
  never drags megabytes along.
- `DocumentConversion` — the converter's markdown output plus how it was
  obtained. Cached, so re-parsing is cheap.
- `ExtractedBlock` — structural blocks with page numbers.

Content:

- `Question` — owned by a `Subject`, not an exam. Carries `ContentHash` for
  deduplication, `HasAnswer`, and `DifficultyConfidence` (zero from extraction,
  one once a reviewer sets it).
- `QuestionExamLink` — which exams may use a question: `direct` for the exam its
  material came from, `associated` for exams that borrow it. Derived data,
  rebuilt whenever associations change, and deliberately not soft-deleted.
- `QualityReview` — a human or model audit.

Work and output: `Job` (one table for every pipeline), `TestPaper`,
`TestPaperItem`.

## The parser

`internal/extract` reads converted markdown and returns structured questions. It
never consults a list of known exams. Instead it samples the document:

1. **Option style detection.** Scores every candidate convention — `(a)`, `a)`,
   `A.`, `(1)`, `(i)` and so on — by counting *runs*: markers appearing in order
   from the first label of their alphabet, either as consecutive line prefixes or
   packed into one line. Stray numbers in prose do not form runs. The modal run
   length is the document's option count.
2. **Question style detection.** The hard case is a paper whose options are
   numbered `1.`–`4.`, because an option marker and a question marker are then
   textually identical. The detector replays the same state machine the parser
   uses — an ascending number opens a question, and the digits that follow fill
   its options — and picks whichever convention yields the longest run of
   ascending question numbers.
3. **Body parse.** A state machine walks the question region, handling multi-line
   stems, options inline or one per line, `Ans.` and `Sol.` lines, and
   "Directions for questions 5 to 9" preambles shared across a range.
4. **Answer backfill.** The answer region is located by requiring both a
   heading that reads like an answer section *and* a dense run of answer pairs
   behind it. Bracketed pairs are trusted over bare ones; grid tables are read
   cell by cell; worked solutions supply answers the key did not.

`ParseResult` reports which conventions were detected and warns about anything
suspicious, so a thin yield is explainable rather than mysterious.

## API

Base path `/api/v1`. Responses are `{"data": …}` with `{"meta": …}` on list
endpoints, or `{"error": "…"}`.

| Method | Path | Purpose |
|---|---|---|
| GET | `/meta/capabilities` | Formats, OCR engines, limits, document kinds |
| GET | `/meta/overview` | Dashboard counts and the job feed |
| GET | `/meta/converter` | Conversion service health |
| GET POST | `/exams` | List, create |
| GET PUT DELETE | `/exams/:id` | Read, update, delete |
| GET POST | `/exams/:id/subjects` | Subjects an exam covers |
| DELETE | `/exams/:id/subjects/:subjectId` | Detach a subject |
| GET POST | `/exams/:id/patterns` | Pattern versions |
| GET PUT DELETE | `/exams/:id/patterns/:patternId` | One version |
| POST | `/exams/:id/patterns/:patternId/activate` | Make it current |
| POST | `/exams/:id/pattern-analysis` | Derive a pattern from papers |
| GET POST | `/exams/:id/associations` | Borrowing rules |
| PUT DELETE | `/exams/:id/associations/:assocId` | Edit, remove |
| POST | `/exams/:id/paper-availability` | Can a paper be built, and what is short |
| POST | `/exams/:id/papers` | Queue paper generation |
| GET POST | `/subjects`, `/topics` | Catalogue |
| GET PUT DELETE | `/subjects/:id`, `/topics/:id` | One entry |
| POST | `/documents` | Multipart upload, one or many files |
| GET | `/documents` | List with filters |
| GET PATCH DELETE | `/documents/:id` | Read, edit metadata, delete |
| GET | `/documents/:id/download` | The original bytes |
| GET | `/documents/:id/conversion` | Converted markdown |
| GET | `/documents/:id/blocks` | Structural blocks |
| POST | `/documents/:id/ingest` | Convert and extract |
| POST | `/documents/:id/reparse` | Re-extract from the cached conversion |
| GET | `/jobs`, `/jobs/:id` | Work feed |
| POST | `/jobs/:id/cancel`, `/jobs/:id/retry` | Control |
| GET POST | `/questions` | Warehouse query, manual create |
| PATCH | `/questions` | Bulk status, difficulty or re-filing |
| GET PUT DELETE | `/questions/:id` | One question |
| POST | `/questions/:id/review` | Approve or reject |
| GET | `/warehouse` | Subject, topic and exam rollups |
| GET | `/papers`, `/papers/:id` | Generated papers |
| PUT DELETE | `/papers/:id` | Publish, delete |
| GET | `/papers/:id/export` | Markdown, with or without the answer key |

## Tests

```bash
go test ./...
```

`internal/extract/parser_test.go` covers the four marker conventions that matter,
including the ambiguous numeric-option case. `internal/server/router_test.go`
builds the router, which is how a route conflict is caught at test time rather
than at boot.
