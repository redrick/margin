package main

const agentHelp = `margin — instructions for the agent working next to the viewer

The reader ran margin (uncommitted changes), margin <branch> or margin <commit>. margin wrote a
review file with one station ("stop") per changed file, each showing every changed region with a
little context, opened the viewer beside you and started you with the review file path.

0. Shape the tour first, before any notes. Edit the review file (it reloads on save, run
   margin lint after):
     summary   what the change does and why, then your overall verdict, in at most 5 short lines
     stations  group related files into a few stops, caller next to callee, declaration next to use
               order them riskiest first: reviewers catch far fewer problems in what they read last
               per stop set  risk: high | medium | low   (security, concurrency, data loss, public
                             API and missing tests raise it)
                             concern: feature | fix | refactor | tests | config | docs
                             tests: [TestNames or test files that cover it]
                             lede: one or two sentences on what this stop is about
     flow      optional ASCII diagram (write flow: |2 if a line starts at column 0)
     skip      [{what, why}] for changes not worth reading
   High-risk stops open with your notes hidden so the reader looks first; that is intended.

1. Annotate. For each meaningful change:
     margin note <file>:<line> --kind <kind> "text"
   kinds: issue (a real problem), question (for the author), ok (checked, looks right),
          info (context the reader needs), nit (minor, never blocking)
   An issue must carry --evidence: the lines, a failing test, or a repro command you ran. Without it
   the viewer shows it as a question. Add --confidence high|medium|low when you are unsure.
   Add a short --kind ok note on every part you checked and found fine, so a part without notes
   really means you did not examine it.
   Use current line numbers (old ones for deleted files); the line must be inside a shown change.
   Keep notes few and useful: one per change worth explaining, not per line.
   Notes are for a human reading this code for the first time: plain, simple, full sentences, never
   telegraphic. Explain a bit more than feels necessary, and add cues where they help: what calls
   this, what it did before, a small input/result example, which note to read first.
   Notes render **bold** and ` + "`code`" + `. Pass - instead of the text to read it from stdin; do that
   whenever the text contains quotes, since shell quoting silently eats apostrophes.
   The viewer marks new notes and the reader jumps to them with N; still say in chat which stop you
   just annotated. A recap stop listing every issue and question is added automatically.

2. Steer when it helps: margin goto <station>[:<note>] or margin goto <file>:<line>.
   margin where prints what the reader is looking at; use it when they say "this" or "here".

3. Questions arrive in your pane as "[margin qN] file:line in station ...: question".
   Answer in chat, then record it so it stays in the review: margin answer qN "answer".

4. Code changes requested during the review: edit the code, add changed: "what and why" to the
   affected notes (the reader sees Δ), and add new notes with margin note.

Review file format
  version: 1
  repo: /abs/path/to/repo
  base: <commit>              head: <commit>, absent when the working tree is shown
  title, kicker, summary, flow
  stations:
    - id: short-id            no spaces or ':'
      title, lede, risk, concern, tests
      parts:
        - file: path
          hunks: true         or func: Name | from: "needle" [to: "needle"] | lines: 10-40 | none = whole file
          side: base          show the base version (deleted code)
          base_file: old/path for renamed files
      notes:
        - at: "text on one line"      file:, nth: pick one of several matches
          kind: issue | question | ok | info | nit
          confidence: high | medium | low
          text: ...
          evidence: ...
          changed: ...                q:, qid: are written by margin answer
  skip: [{what, why}]    renames: [{before, after}]    tests: [{file, why}]

margin never writes to the reviewed repo and never runs anything from it.
`
