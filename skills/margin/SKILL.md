---
name: margin
description: Review code changes with margin, a viewer that shows the diff with your notes beside each line while a person reads it. Use when the user asks for a margin review of uncommitted changes, a branch, a commit or a pull request; when a session starts with "Code review with margin"; or when a message starts with "[margin q", "[margin comments]", "[margin fix]", "[margin reply]", "[margin run]" or "[margin tryout]".
---

# margin

margin shows a person a git change as a guided review: stops in reading order, the diff, and the
notes you write beside the lines. You shape the tour and write the notes; the person reads, asks
and comments, and margin types their questions and comments into your session.

The command is `margin` when margin started you. In a session margin did not start, the same
commands are `margin-tui` (or `margin-gui` with `MARGIN_CLI=1` set), for example
`margin-tui agent-help`.

## Started by margin

Your first message says "Code review with margin" and names the review file. Run the `margin brief`
it gives you and follow it; `margin agent-help` has the full instructions and the review file
format. Read `margin agent-help` once per session before writing notes.

## Asked for a review in this session

1. Create the review file, without opening a viewer:

       margin-tui prepare              # uncommitted changes
       margin-tui prepare --staged     # only what is staged
       margin-tui prepare feature-x    # a branch against the main branch
       margin-tui prepare a1b2c3d      # one commit
       margin-tui prepare --pr 123     # a GitHub pull request (its head must be fetched)

   Add `--intent "what the change was meant to do"` when the commits do not say. It prints the
   review file path and whether the review is new.
2. Tell the person to open it beside you, in a window or terminal of their own:

       margin-gui <review file>        # the desktop app
       margin-tui open <review file>   # the terminal viewer

3. Run `margin-tui brief --review <review file>` (add `--new` when prepare said the review is
   new) and follow what it prints.

Inside tmux, `margin-tui <target>` run from your shell does all of this at once: it opens the
viewer in a pane beside yours and sends you the instructions.

## Messages from the reader

- `[margin qN] …` is a question about a line. Answer it in chat, then record the answer with
  `margin answer qN "answer"` so it stays in the review.
- `[margin comments] …` is a batch of review comments. Change code only for a comment that
  explicitly asks for it. Answer every comment in the comments note the batch names, then close
  it with `margin resolve cN "short answer"`.
- `[margin fix] …` lists what the reader wants changed: problems you pointed out that they agree
  with, and stops they judged as needing changes (their comments on the stop say what; with none,
  ask them first). Fix each in the code, update the affected notes, answer it in the comments note
  and close it with `margin resolve cN "Fixed: ..."`. If one should not be fixed after all, leave
  it and say why.
- `[margin reply] …` is the reader answering your answer in the comments note. Reply under the
  same comment in that note, and resolve again if your answer changes.
- `[margin run] …` asks you to run tests and report what they show; change nothing.
- `[margin tryout] …` reports a change the reader tried on a line or block and which tests failed because of
  it. Say what that shows about the tests and the line (a check no test notices is a testing gap);
  change nothing unless asked. While the reader tries a change, the file briefly holds their edit;
  margin puts the line back after.

Never commit, push or run another git write command unless the person asks.
