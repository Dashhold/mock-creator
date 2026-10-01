// Dashhold-EdTech question booklet (Typst 0.15.1).
//
// The data file comes from internal/render (Document, model.go); its path is
// passed as --input data=jobs/<id>/data.json.
//   - the font list ends in Noto Sans Math so every symbol has a glyph
//   - question and passage breakability, option columns and key columns are
//     computed in Go
//   - every printed label comes from data.labels
// Data is only ever inserted as text: no eval, read or import.

#let data = json(sys.inputs.data)
#let fonts = ("Noto Sans", "Noto Sans Devanagari", "Noto Sans Bengali", "Noto Sans Gujarati",
  "Noto Sans Gurmukhi", "Noto Sans Tamil", "Noto Sans Telugu", "Noto Sans Kannada",
  "Noto Sans Malayalam", "Noto Sans Oriya", "Noto Sans Math")
#let brand = data.brand
#let labels = data.labels
#let accent = rgb(data.accent)
#let accent-soft = accent.lighten(88%)
#let muted = luma(95)
#let rule = luma(185)

// Go's encoding/json writes a nil slice as null; treat it as empty.
#let arr(x) = if x == none { () } else { x }

#set document(title: data.title, author: brand.name, keywords: (data.code,))
// Text boxes span the full ascender/descender so Devanagari matras above the
// headline and below the baseline never reach into the next block.
#set text(font: fonts, size: 10pt, lang: data.lang, top-edge: "ascender", bottom-edge: "descender")
#set par(leading: 0.28em, spacing: 0.7em, justify: false)

// Plain text with the line breaks the source had.
#let lines(s) = s.split("\n").map(p => [#p]).join(linebreak())

// Alt text is required for PDF/UA-1 and read out by screen readers.
#let logo(h) = image(brand.logo, height: h, alt: brand.name + " logo")

#let code-text = data.code + (if data.revision > 1 { " (Rev " + str(data.revision) + ")" } else { "" })

#let watermark = {
  if data.draft {
    place(center + horizon, rotate(-38deg, text(72pt, weight: "bold", fill: rgb(198, 40, 40, 34))[DRAFT]))
  } else if data.watermark {
    place(center + horizon, rotate(-38deg, text(46pt, weight: "bold", fill: accent.transparentize(93%), brand.name)))
  }
}

#set page(
  paper: "a4",
  margin: (top: 22mm, bottom: 18mm, x: 15mm),
  header-ascent: 28%,
  footer-descent: 30%,
  background: watermark,
  header: context {
    if here().page() > 1 {
      grid(
        columns: (auto, auto, 1fr),
        column-gutter: 5pt,
        align: (left + horizon, left + horizon, right + horizon),
        logo(6.5mm),
        text(9pt, weight: "bold", fill: accent, brand.name),
        {
          // From the answer key on, the running label switches to the key label.
          let key-start = query(<key-start>)
          let label = if key-start.len() > 0 and here().page() >= key-start.first().location().page() {
            data.key_label
          } else { data.part_label }
          text(8.5pt, fill: muted, data.running_title + "  ·  " + label)
        },
      )
      v(-5pt)
      line(length: 100%, stroke: 0.6pt + accent)
    }
  },
  footer: context {
    line(length: 100%, stroke: 0.4pt + rule)
    v(-5pt)
    set text(7.8pt, fill: muted)
    grid(
      columns: (1fr, 1fr, 1fr),
      align: (left, center, right),
      [#labels.paper_code: #code-text],
      [Page #counter(page).display() of #counter(page).final().first()],
      [© #brand.name],
    )
  },
)

// --- building blocks --------------------------------------------------------

#let draft-banner = if data.draft {
  block(width: 100%, fill: rgb("#fdecea"), stroke: 1pt + rgb("#c62828"), inset: 8pt, radius: 3pt, below: 10pt,
    text(fill: rgb("#b71c1c"), weight: "bold", size: 9.5pt)[DRAFT - NOT CLEARED FOR DELIVERY. #data.draft_reason])
}

#let brand-block(subtitle) = {
  grid(
    columns: (auto, 1fr),
    column-gutter: 10pt,
    align: (left + horizon, left + horizon),
    logo(19mm),
    stack(spacing: 5pt,
      text(21pt, weight: "bold", fill: accent, brand.name),
      text(9.5pt, fill: muted, subtitle)),
  )
  v(4pt)
  line(length: 100%, stroke: 1.4pt + accent)
  v(2pt)
}

#let meta-cell(label, value) = [#text(7.5pt, fill: muted, upper(label)) \ #text(10pt, weight: "bold", value)]

#let meta-table = table(
  columns: (1fr, 1fr, 1fr),
  stroke: 0.5pt + rule,
  inset: (x: 7pt, y: 6pt),
  meta-cell(labels.paper_code, code-text), meta-cell(labels.date, data.date), meta-cell(labels.duration, data.duration),
  meta-cell(labels.questions, str(data.question_count)), meta-cell(labels.max_marks, data.max_marks), meta-cell(labels.negative, data.negative),
)

#let field-line = box(width: 1fr, stroke: (bottom: 0.5pt + luma(120)), height: 1.1em)
#let roll-boxes = box(stack(dir: ltr, spacing: 0pt, ..range(10).map(_ => box(width: 6.2mm, height: 6.6mm, stroke: 0.5pt + luma(120)))))

#let candidate-box = block(width: 100%, stroke: 0.6pt + rule, inset: 9pt, radius: 2pt, below: 10pt)[
  #grid(columns: (auto, 1fr), column-gutter: 8pt, row-gutter: 11pt, align: (left + bottom, left + bottom),
    [#labels.candidate_name:], field-line,
    [#labels.roll_no:], roll-boxes,
    [#labels.signature:], box(width: 60%, stroke: (bottom: 0.5pt + luma(120)), height: 1.1em),
  )
]

#let section-table = table(
  columns: (auto, 1fr, auto, auto, auto),
  stroke: 0.5pt + rule,
  inset: (x: 6pt, y: 4.5pt),
  fill: (_, y) => if y == 0 { accent-soft },
  table.header(strong(labels.col_part), strong(labels.col_section), strong(labels.col_questions),
    strong(labels.col_marks_each), strong(labels.col_q_nos)),
  ..data.sections.map(s => (s.label, s.name, str(s.count), s.marks, str(s.from) + "–" + str(s.to))).flatten(),
)

// Label and text sit in one paragraph so they share a baseline whatever the
// script; a hanging indent keeps wrapped lines aligned under the text.
#let option-cell(o) = par(hanging-indent: 2.1em)[#box(width: 2.1em, text(weight: "bold")[(#o.label)])#lines(o.text)]

#let question(q) = block(breakable: q.breakable, width: 100%, below: 9pt)[
  #par(hanging-indent: 8mm)[#box(width: 8mm, text(weight: "bold")[#q.number.])#if q.missing { text(fill: muted, q.stem) } else { lines(q.stem) }#if q.marks_note != "" { h(4pt); text(8.5pt, fill: muted, q.marks_note) }]
  #let opts = arr(q.options)
  #if opts.len() > 0 {
    v(1pt, weak: true)
    pad(left: 8mm, grid(columns: (1fr,) * q.cols, column-gutter: 10pt,
      row-gutter: if q.cols == 1 { 3pt } else { 5pt }, ..opts.map(option-cell)))
  }
]

#let passage-box(p) = block(width: 100%, fill: luma(246), stroke: (left: 2pt + accent), inset: 8pt, below: 7pt)[
  #text(weight: "bold", labels.directions + " (Q. " + str(p.from) + "–" + str(p.to) + "): ") #lines(p.text)
]

#let section-header(s) = {
  block(width: 100%, fill: accent, inset: (x: 8pt, y: 6pt), radius: 2pt, below: 5pt, sticky: true,
    grid(columns: (1fr, auto), align: (left + horizon, right + horizon),
      text(fill: white, weight: "bold", 10.5pt, s.label + ": " + s.name),
      text(fill: white, 8.5pt, "Q. " + str(s.from) + "–" + str(s.to) + "  ·  " + str(s.count) + " questions")))
  // Regular weight, muted: no italic face is shipped.
  block(below: 9pt, sticky: true, text(8.8pt, fill: muted, s.note))
}

// --- question paper ----------------------------------------------------------

#if data.part != "key" {
  brand-block(data.subtitle)
  align(center, text(15pt, weight: "bold", data.title))
  v(-4pt)
  align(center, text(10.5pt, fill: muted, data.exam))
  v(4pt)
  draft-banner
  meta-table
  v(6pt)
  candidate-box
  text(11pt, weight: "bold", fill: accent, labels.general_instructions)
  v(-2pt)
  enum(tight: false, spacing: 6pt, ..arr(data.instructions).map(i => [#i]))
  v(4pt)
  section-table
  v(1fr)
  align(center, text(9pt, weight: "bold", fill: muted, labels.do_not_open))
  pagebreak()

  for s in data.sections {
    section-header(s)
    for b in s.blocks {
      if b.kind == "passage" {
        // The directions travel with their first question unless Go marked the passage breakable.
        block(breakable: b.passage.breakable, below: 9pt)[
          #passage-box(b.passage)
          #question(b.questions.first())
        ]
        for q in b.questions.slice(1) { question(q) }
      } else {
        question(b.question)
      }
    }
  }
}

// --- answer key and solutions -----------------------------------------------------

#if data.part != "paper" {
  if data.part == "both" { pagebreak() } else {
    brand-block(data.subtitle)
    align(center, text(15pt, weight: "bold", data.title))
    v(-4pt)
    align(center, text(10.5pt, fill: muted, data.exam + "  ·  " + data.key_label))
    v(4pt)
    draft-banner
  }
  [#metadata("key") <key-start>]
  text(13pt, weight: "bold", fill: accent, labels.answer_key)
  v(-2pt)
  for k in arr(data.key) {
    block(below: 4pt, sticky: true, text(9.5pt, weight: "bold", k.section))
    table(
      columns: (1fr,) * k.cols,
      align: center,
      inset: (x: 2pt, y: 4pt),
      stroke: 0.5pt + rule,
      ..k.answers.map(a => [#text(fill: muted, str(a.n)) #h(2pt) #text(weight: "bold", a.a)]),
    )
    v(4pt)
  }
  let sols = arr(data.solutions)
  if sols.len() > 0 {
    v(6pt)
    text(13pt, weight: "bold", fill: accent, labels.solutions)
    v(-2pt)
    for sol in sols {
      block(below: 8pt)[
        #text(weight: "bold")[#sol.n. #if sol.a != "" { labels.answer + ": (" + sol.a + ")" }] #h(4pt) #lines(sol.text)
      ]
    }
  }
}
