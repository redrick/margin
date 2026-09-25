package main

const agentHelp = `margin — instructions for the agent working next to the viewer

The reader ran margin (uncommitted changes, or --staged), margin <branch>, margin <commit> or
margin --pr N. margin wrote a
review file with one station ("stop") per changed file, each showing every changed region with a
little context, opened the viewer beside you and started you with the review file path.

0. Shape the tour first, before any notes. Edit the review file (it reloads on save, run
   margin lint after):
     asked     what the change was meant to do, written by margin from the commit messages, the
               pull request or the reader's --intent. Read it; do not edit it.
     did       what the change actually does, in two or three plain sentences
     gap       where the change does more, less or something other than asked: scope that crept
               in, a part of the ask that is missing, behaviour nobody asked for. Write "none" when
               it matches; when asked is empty, say the intent is unknown and what you assumed.
     motivation, outcome   optional, one sentence each, for someone in their first week: what was
               annoying, broken or missing for a person, and what is better for them now. No code
               words. Leave them out rather than guess.
     summary   what the change does and why, then your overall verdict, in at most 5 short lines
     complexity  one line: low | medium | high | very high, and why ("new schema plus two services")
     stations  group related files into a few stops, caller next to callee, declaration next to use
               order them riskiest first: reviewers catch far fewer problems in what they read last,
               but keep a stop that introduces a type before the stop that uses it
               per stop set  risk: high | medium | low
                             risk is how bad it would be if the reader missed a problem here, not
                             how likely a bug is. High: security, auth, data loss or migration,
                             concurrency, money, public API, a production dependency (with its
                             lockfile). Low: docs, dev-only tooling, test fixtures. A stop mixing
                             both takes the risk of its riskiest change.
                             risk_why: the reasons in a few plain words ("runs under the store
                                    lock; changes a public signature"), never line counts
                             concern: feature | fix | refactor | tests | config | docs
                             tests: [TestNames or test files that cover it]
                             lede: one or two sentences on what this stop is about
                             flow: optional ASCII diagram when the stop wires several parts together
               and open every stop with its rationale, a short paragraph each, in plain sentences:
                             scope: why this code is a stop of its own: what ties its parts
                                    together and why it is not part of a neighbouring stop
                             what:  what the code does and how it does it, step by step, as the
                                    reader will meet it in the parts below
                             why:   why it changed, and why it is built this way rather than
                                    the obvious alternative
               per part set  about: one sentence on what this part shows and why it is in the stop
               Moved code: a function moved to another file is one change; put both ends in one
               stop. The viewer marks lines moved unchanged and folds them.
     flow      optional ASCII diagram for the whole change (write flow: |2 if a line starts at column 0)
     skip      [{what, why}] for changes not worth reading. Put the path or a glob in what
               (what: go.sum) and the file counts as placed.
   Every changed line has to be in a stop or in skip. margin lint lists what is missing, and the
   viewer shows it in an extra stop "Changes no stop shows" until you place it. Files matched by
   the repository's .margin/ignore are listed on the overview and need no stop.
   High-risk stops open with your notes hidden so the reader looks first; that is intended.

1. Annotate. For each meaningful change:
     margin note <file>:<line> --kind <kind> "text"
   kinds: issue (a real problem), question (for the author), ok (checked, looks right),
          info (context the reader needs), nit (minor, never blocking),
          decide (a call only the reader can make, see below)
   decide notes are the reader's checklist before approving. Use one only for a judgment that
   needs product context, team conventions or the author's intent, which neither you nor a test
   can settle: "Should retryCount reset when the user switches orgs?". Phrase it as a question,
   put it on the exact line it depends on, and leave anything a linter, a test or you can check
   out of it. Most stops need none; never add one just to have one.
   Add --focus <area> when a note is about one of: security, breaking-change, data-integrity,
   concurrency, performance, complexity, architecture, new-pattern, testing-gap. The reader can
   filter by it.
   An issue must carry --evidence: the lines, a failing test, or a repro command you ran. Without it
   the viewer shows it as a question. Add --confidence high|medium|low when you are unsure.
   Add a short --kind ok note on every part you checked and found fine, so a part without notes
   really means you did not examine it.
   Use current line numbers (old ones for deleted files); the line must be inside a shown change.
   Annotate generously; the reader reads the code with your notes beside it and wants many of
   them. Put a note on every changed block of a few lines: what it does, why it is there, and what
   would go wrong without it. Add notes on unchanged lines too where the reader needs them to
   follow the change (the caller that now behaves differently, the check that makes a new line
   safe). A stop with long code should have notes spread through it, not only at the top.
   Skip only lines that explain themselves: imports, a rename the lede already covers.
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
   Review comments arrive as a batch, "[margin comments] ...", one line per comment cN. They are
   the reader's own remarks: handle each one, by changing the code or by explaining why not,
   then close it with margin resolve cN "Fixed: ..." or "Not changed, because ...".
   margin questions lists what is still open.

4. Code changes requested during the review: edit the code, add changed: "what and why" to the
   affected notes (the reader sees Δ), and add new notes with margin note.

5. Test coverage. The reader sees which tests run which changed lines, so measure it once the tour
   is shaped, and again after code changes (the viewer shows older coverage as stale).
     Go     margin coverage go writes a script next to the review. Read it, then run it with sh.
            It runs each test of the changed packages on its own and records what each one runs.
            Add packages whose tests exercise the change from elsewhere to a run_pkgs line.
     other  measure per-test line coverage with the project's own tool (for example pytest
            --cov-context=test with coverage json --show-contexts, or one run per test file),
            convert it and pass it to margin coverage add <file.json>, or - for stdin:
              {"tests": [{"name": "test_total", "file": "tests/test_a.py",
                          "lines": {"src/a.py": [12, 13, 20]}}],
               "executable": {"src/a.py": [10, 12, 13, 15, 20]}}
            Lines are 1-based. executable is optional; it separates lines no test ran from lines
            that cannot run at all. --replace drops the tests you added before.
   Both print the changed lines no test runs. Add a note to the ones that matter: a --kind question
   when a test is probably missing, or an issue with evidence when the untested line is risky.
   Coverage says a test ran a line, not that it checked the result; say so in a note where a test
   runs a line without asserting anything about it. margin coverage clear starts over.

Review file format
  version: 1
  repo: /abs/path/to/repo
  base: <commit>              head: <commit>, absent when the working tree is shown
  staged: true                the current side is the index
  title, kicker, asked, asked_from, instructions   (written by margin)
  did, gap, motivation, outcome, summary, complexity, flow
  stations:
    - id: short-id            no spaces or ':'
      title, lede, scope, what, why, risk, risk_why, concern, tests, flow
      parts:
        - file: path
          about: "one sentence"
          hunks: true         or func: Name | from: "needle" [to: "needle"] | lines: 10-40 | none = whole file
          side: base          show the base version (deleted code)
          base_file: old/path for renamed files
      notes:
        - at: "text on one line"      file:, nth: pick one of several matches
          kind: issue | question | decide | ok | info | nit
          focus: security | breaking-change | data-integrity | concurrency | performance |
                 complexity | architecture | new-pattern | testing-gap
          confidence: high | medium | low
          text: ...
          evidence: ...
          changed: ...                q:, qid: are written by margin answer and margin resolve
  skip: [{what, why}]    renames: [{before, after}]    tests: [{file, why}]

margin never writes to the reviewed repo and never runs anything from it: you run the tests.
`
