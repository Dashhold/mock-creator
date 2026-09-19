# Mock Creator

An exam-agnostic content engine. Give it documents, it gives you question banks
and generated test papers.

The engine knows nothing about any particular examination. It works out each
document's own conventions — how questions are numbered, how options are
labelled, how many there are, where the answer key sits — and it learns each
exam's structure from the papers you feed it. Supporting a new exam means adding
data, not changing the software.

Nothing reaches a generated paper unvalidated. Every question carries a quality
verdict, every verdict carries the evidence behind it, and a paper that fails its
checks cannot be published or exported. See [Quality](#quality) below.

## Quick start

Requires Docker Desktop, roughly 8 GB of RAM, and ports 3000, 8090, 5001 and
5432 free.

```powershell
.\start.ps1
```

```cmd
start.bat
```

Then open <http://localhost:3000>.

The first build is slow. The conversion service bakes several hundred megabytes
of layout, table and OCR models into its image so that nothing is downloaded at
runtime. Expect 10–20 minutes once, then seconds on later starts.

Stop with `.\stop.ps1` or `stop.bat`. Your data survives in the `pgdata` volume.

## How the workflow goes

1. **Create an exam.** Just a name. There is no built-in list of exams.
2. **Give it a pattern.** Either type the section breakdown in, or drop in one or
   more past papers and let the engine derive it: which sections exist, how many
   questions each carries, the weightage, the option count, and — when the paper
   prints them — the duration and marking scheme. Patterns are versioned and
   carry a confidence score, because a pattern inferred from one paper is a
   weaker claim than one confirmed across four.
3. **Associate other exams**, optionally. A brand new exam has no questions of
   its own. Pointing it at an exam that does, optionally for one subject only,
   makes that exam's questions available to it immediately.
4. **Upload documents.** Question papers, answer keys, solution booklets, books,
   notes, syllabi. PDF, Word, PowerPoint, Excel, HTML, Markdown, text, CSV,
   EPUB, ODF and images. Scanned pages go through OCR automatically.
5. **Browse the warehouse.** Every question, filed under its subject, tagged
   with every exam that can use it, and marked with its quality verdict.
6. **Work the review queue.** Whatever the checks could not clear waits on the
   Review page, worst first, with the source lines it was read from. Accepting or
   rejecting is one click and is recorded against your name.
7. **Generate papers.** The generator draws only on questions that passed, fills
   each section of the pattern, honours weightage and difficulty mix, and tells
   you which subject ran short and what to upload if it could not finish. The
   finished paper gets its own QA report before it can be published.

## What runs where

| Service | Port | What it does |
|---|---|---|
| `frontend` | 3000 | React app served by nginx, which also proxies the API |
| `api` | 8090 | Go API plus the background worker pool |
| `converter` | 5001 | Python service: geometric PDF reading, OCR, table extraction |
| `db` | 5432 | PostgreSQL. Holds the uploaded files themselves, not just metadata |
| `ollama` | 11434 | Optional local model for the judgement-based checks. Off unless you start the `llm` profile |

Uploaded documents are stored in the database. There is no directory to copy
files into and no volume to keep in sync — the only way material enters the
system is through the API, which means it can always be re-read, re-converted
and re-parsed later.

## Design notes worth knowing

**Nothing blocks on conversion.** Uploading returns immediately and creates a
job. Workers claim jobs with `SELECT … FOR UPDATE SKIP LOCKED`, report progress
as they go, and survive a restart: anything interrupted mid-flight is requeued.
The UI tracks the same job feed from every page.

**Conversions are cached.** The expensive part is OCR. Re-extracting questions
after you improve a subject's aliases reuses the stored conversion and only
re-runs the parser, which takes moments rather than minutes.

**Subject recognition is data, not code.** Every subject carries a list of
aliases — the headings it is printed under in real documents. When a heading
matches nothing, its questions land under *Unsorted* rather than disappearing;
add the heading as an alias on the Taxonomy page and re-parse.

**Questions belong to subjects, not exams.** Which exams may use a question is a
separate set of rows: a direct link to the exam its material came from, plus
inherited links for every exam that borrows from it. That is why one question can
serve six exams without being copied six times, and why the same paper uploaded
twice does not double the bank — questions are deduplicated on a normalised
fingerprint of the stem and options.

**Difficulty is honest about itself.** Machine extraction cannot judge how hard a
question is, so it records `medium` with zero confidence and generated papers say
so instead of implying the requested difficulty mix was met. A reviewer setting
the difficulty makes it authoritative.

## Quality

A paper is only as good as the text it was read from, so the quality work starts
at extraction and ends at publication. Four stages, each of which can hold
content back but none of which can silently repair it.

### 1. Reading the PDF

Most exam papers are born-digital: the PDF already states which character sits at
which coordinate. The default engine reads those coordinates directly — grouping
glyphs into rows, finding the column gutter from where the page has no ink,
ordering the columns, then emitting text. It is deterministic, and on a two-column
paper it is also the only approach that gets the reading order right. A layout
model has to *infer* that order from the picture and interleaves the two columns,
which produces text that looks plausible and is nonsense: a stem from the left
column with options from the right, page headers spliced into option 4.

That is worth being concrete about, because it was the single cause of most
corruption in this system's output before:

| | layout model | geometry |
|---|---|---|
| Time for a 24-page paper | 41 s | 0.5 s |
| Reading order on two columns | interleaved | correct |
| Same input, same output | no | yes |

Scanned pages have no text layer, so they still go through Docling and OCR.
A document that is partly scanned gets handled page by page, and the OCR output is
spliced into the geometric output for just those pages. Set `CONVERTER_ENGINE` to
`geometry` or `docling` to override the choice.

Extraction reports what it could not do rather than papering over it. A glyph the
PDF gives no character for becomes U+FFFD and is counted — not deleted, because
deleting it would hide that something is missing. Stacked fractions cannot be
linearised into a single line of text, so they are counted and warned about, never
guessed at. Each conversion carries a confidence score, and anything below
`CONVERTER_MIN_CONFIDENCE` sends everything it produced to review.

### 2. Rules

Every question is checked by `internal/quality` before it is stored: unreadable
characters, mojibake, control bytes, truncated or runaway stems, options that
repeat each other or are bare labels, a missing or out-of-range answer, a stem
that references a passage that is not attached, contamination from a neighbouring
question, near-duplicates against the rest of the warehouse.

Severity follows the field, not the defect. Corruption in a stem, an option or a
passage is critical because it changes what is being asked; the same corruption in
an explanation is major, because the question still stands. Every finding carries
a code, a severity, its source and the evidence that triggered it.

### 3. A model, if you configure one

Some judgements are not expressible as rules: whether a question is actually
answerable from what it states, whether an explanation contradicts the answer key,
whether two rewordings are the same question. Those go to a language model when
one is configured.

The model is not trusted. It is used to *find* problems, never to invent content.
Every fragment it returns is checked against the source text, and if any of it
cannot be located there, the whole reply is discarded and the question is flagged
for a person instead. Severities have floors, so the model cannot downgrade
"not answerable" to a minor note. And a model that is configured but unreachable
is recorded as a problem, not skipped — see `GET /api/v1/meta/model`.

With no model configured, the model-only checks report themselves as *not run*.
They never count as passes.

### 4. The gate

Each question ends up in one of four states, and the distinction between the last
two is the point of the whole exercise:

| Status | Meaning |
|---|---|
| `pass` | cleared for use in generated papers |
| `review` | usable content, but something needs a person's eye |
| `failed` | critical defect; will not be used |
| `unchecked` | not yet validated — **not a pass** |

Paper assembly selects on `quality_status = 'pass'` in SQL, so `unchecked` cannot
leak through by way of an application-layer oversight. Finished papers get their
own verdict — `pass`, `review` or `failed` — covering section counts against the
pattern, marks, duplicate questions within the paper, answer-option balance and
difficulty coverage. A paper with a critical finding cannot be published and
cannot be exported; both return HTTP 409 with the reasons.

Across six real SSC CGL papers — 555 extracted questions — that works out to 64%
cleared, 21% held for review and 14% rejected. The held-back share is deliberate.
Lowering it means reviewing questions, not loosening checks.

### Provenance

`GET /api/v1/questions/:id/provenance` returns, for one question: the document and
page it came from, which engine read it, the extraction confidence, the exact
source lines with surrounding context, every quality finding, which rules version
produced them, whether a model was involved and which one, near-duplicates, and
the full human review history. That is enough to settle a disagreement with the
extractor without opening the PDF.

### The review API

| Route | Purpose |
|---|---|
| `GET /api/v1/review` | queue, worst first; filter by status, subject, document, exam, issue code, severity |
| `GET /api/v1/review/summary` | counts by status, by subject and by defect code |
| `POST /api/v1/review/requalify` | re-run the checks after a rules change |
| `POST /api/v1/questions/:id/resolve` | `accept`, `reject` or `recheck`, with an optional difficulty rating |
| `GET /api/v1/papers/:id/qa` | a paper's report |
| `POST /api/v1/papers/:id/qa` | re-run it |
| `GET /api/v1/meta/model` | is a model configured, and is it reachable |

Accepting a flagged question does not erase the flags. It adds a `human_override`
record naming the reviewer, so the audit trail shows a person made the call.

### Turning on a model

Bundled but off by default, because it is a large image and the pipeline is
useful without it:

```powershell
docker compose --profile llm up -d
docker compose exec ollama ollama pull qwen3:30b-a3b-instruct-2507-q4_K_M
```

Then set `LLM_BASE_URL=http://ollama:11434/v1`,
`LLM_MODEL=qwen3:30b-a3b-instruct-2507-q4_K_M` and `OLLAMA_MEMORY_LIMIT=24G` in
`.env`, and restart the API.

That model is a mixture of experts: 19 GB on disk, but only about 3B parameters
run per token, so on CPU it is faster than a dense 14B while judging better. On a
smaller machine use `qwen3:14b` (9 GB, 12G limit) or `qwen3:4b-instruct` (2.5 GB,
6G). A weaker model finds fewer problems; it cannot introduce wrong ones, because
grounding is checked in code.

Any OpenAI-compatible endpoint works, hosted ones included. Local keeps licensed
exam content on your own hardware, which matters when auditing a warehouse means
tens of thousands of calls over material you licensed.

## Running without Docker

```powershell
# conversion service
cd services\converter
pip install -r requirements.txt
docling-tools models download
uvicorn app.main:app --port 5001

# API (needs PostgreSQL running, and a database called mockcreator)
cd backend
copy .env.example .env
go run ./cmd/server

# frontend
cd frontend
npm install
npm run dev
```

## Configuration

Copy `.env.example` to `.env` to override anything. Every value has a working
default. The settings most worth knowing:

- `CONVERTER_ENGINE` — `auto` reads the PDF's text layer when it has one and OCRs
  when it does not. `geometry` always reads the text layer and fails if there is
  none. `docling` always uses the layout model.
- `CONVERTER_OCR_MODE` — `auto` measures each PDF's text layer and only OCRs
  pages that need it. `force` OCRs everything, which is what poor photocopies
  want. `off` never OCRs.
- `CONVERTER_OCR_ENGINE` — `rapidocr` (bundled, CPU-only, PP-OCR models),
  `easyocr`, or `tesseract_cli`. The service probes each one at startup and only
  advertises what will actually run, so a scanned upload cannot fail on a missing
  engine.
- `CONVERTER_MIN_CONFIDENCE` — conversions scoring below this are still ingested,
  but everything they produce goes to review rather than being usable. 0.75 by
  default.
- `CONVERTER_CONCURRENCY` and `CONVERTER_MEMORY_LIMIT` — conversion is the
  memory-hungry part. Raise them together or not at all.
- `UPLOAD_MAX_BYTES` — 128 MiB by default. Going past 256 MiB also needs
  `client_max_body_size` raised in `frontend/nginx.conf`.
- `REVIEW_AUTO_APPROVE_EXTRACTED` — questions lifted from a real question paper
  arrive approved, on the grounds that whoever set the paper already vetted them.
  This applies only to questions that pass every check; anything with an open
  issue waits for a person regardless. Set to `false` to review every one by hand.
- `LLM_BASE_URL`, `LLM_MODEL`, `LLM_API_KEY` — the optional model reviewer. Unset
  means the model-only checks report as not run. `LLM_AUDIT_QUESTIONS`,
  `LLM_AUDIT_PAPERS`, `LLM_REPAIR_BOUNDARIES` and `LLM_JUDGE_DUPLICATES` control
  which reviews it may perform.

## Security

The API ships **unauthenticated**, which is only appropriate on a private
network or a single machine. Before exposing it further:

- Set `API_KEY` in `.env`, and build the frontend with a matching `VITE_API_KEY`.
- Change `DB_PASSWORD`, and remove the `db` port mapping from
  `docker-compose.yml` so PostgreSQL is reachable only from the other
  containers.
- Put it behind TLS.

## Checking it works

With the stack up:

```powershell
.\scripts\smoke-test.ps1
```

This exercises the parts that only fail at runtime — migrations, the aggregate
queries, association link materialisation, the background worker, paper
selection and export. It creates its own exams, questions and paper, then
deletes them, so it is safe to run against a database you care about.

Document conversion is not covered by it. For that:

```powershell
powershell -ExecutionPolicy Bypass -File .\scripts\verify-quality.ps1 -Papers 6
```

This uploads real PDFs (`-SourceDir`, default `backend\source\pyq\ssc-cgl`),
waits for conversion and extraction,
then asserts on what actually landed in the database: that the geometric engine
was chosen, that confidence and parse quality cleared their thresholds, that the
expected number of questions came out, and — question by question across every
deliverable one — that there are no unreadable characters, no placeholder text, no
content from a neighbouring question, no missing answers, no truncated stems, no
duplicate options and no passage inlined into a stem. It then builds a paper and
checks the gate refuses to publish or export it while a critical finding stands.
77 assertions, currently all passing.

The Go tests cover the parser, the quality rules, model grounding and the route
table. The quality tests are built from the real defects this pipeline produced
before it was fixed, so a regression shows up as a failing test rather than as a
bad paper:

```powershell
cd backend
go test ./...
```

## Database maintenance

```powershell
# migrate only, then exit
docker compose run --rm api ./server -migrate

# drop every table and rebuild the schema (destructive)
docker compose run --rm api ./server -reset

# remove tables from the pre-restructure schema
docker compose run --rm api ./server -drop-legacy
```

## Repository layout

```
backend/              Go API and background workers
  internal/extract/     exam-agnostic question parser
  internal/quality/     deterministic validation: text, question, paper, duplicates
  internal/llm/         optional model reviewer, with grounding enforced in code
  internal/pattern/     pattern inference from real papers
  internal/blueprint/   paper assembly, drawing only on questions that passed
  internal/pipeline/    job workers: convert, extract, analyse, audit, QA, build
  internal/converter/   client for the Python service
  cmd/peek/             inspect a converted document from the command line
services/converter/
  app/geometry.py       the geometric PDF reader
  app/engine.py         engine choice, OCR probing, page-level splicing
frontend/             React + Vite + Tailwind
scripts/              smoke test and end-to-end quality verification
docker-compose.yml    the whole stack; `--profile llm` adds a local model
```

`cmd/peek` is useful when a document parses oddly and you want to see why without
going through the API:

```powershell
cd backend
go run ./cmd/peek -file paper.md -quality -issues
go run ./cmd/peek -file paper.md -q 47 -v
```

## Licence

MIT.
