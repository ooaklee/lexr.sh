# Write documentation

Documentation becomes hard to trust when the README, a website and a wiki can
all disagree. The Markdown in this repository is Lexr's canonical source. A
future MkDocs site or repository wiki should be generated from these files; it
must not become a separately edited authority.

## Put the reader's task in the right place

- Keep the root [`README.md`](https://github.com/ooaklee/lexr.sh#readme) as the short explanation of why
  Lexr exists, how to get it, the first successful path and links onwards.
- Use getting-started pages for the shortest safe route to a first result.
- Use user guides for one operator job, from prerequisites through checking the
  outcome.
- Use operator pages for trusted build, release and publication work.
- Use developer pages for codebase changes and documentation maintenance.
- Use reference pages for stable command, requirement and compatibility facts.
- Use ADRs for accepted, lasting decisions and their consequences.

Link to the canonical detail instead of copying it into several pages. In
particular, [`CONTRIBUTING.md`](https://github.com/ooaklee/lexr.sh/blob/main/CONTRIBUTING.md) owns the contribution,
privacy, commit, pull request and licence rules.

## Use a task-first page shape

Every page should have exactly one level-one heading. After it, lead with the
problem the reader is trying to solve and the useful outcome. Add only the
sections the task needs, normally in this order:

1. prerequisites and the current support boundary;
2. a small sequence of commands or actions;
3. the evidence that shows the step worked;
4. recovery, privacy or qualification limits beside the relevant action; and
5. the next focused guide.

Prefer concrete verbs and short explanations over a feature inventory. Address
the reader as “you”, use British English, define unavoidable technical terms,
and avoid claims such as “supported” or “safe” unless the code and hardware
evidence establish exactly what that means.

## Keep safety close to the action

Do not hide a destructive warning in a general preface. Put whole-device erase
guidance beside the write command, private-data guidance beside collection and
capture steps, and receipt retention beside the mutation that creates it.
Structural validation must not be described as hardware qualification. Dry
runs should say what they inspect and what they deliberately do not change.

## Write links that work in every future rendering

Use relative Markdown links between pages beneath `docs`, rather than site-only
routes. Link to files outside the MkDocs `docs_dir` with their canonical GitHub
URL so they work both in the repository and the generated site. Link to the
source file, not to a generated wiki URL. Use descriptive link text so the
sentence remains useful outside a rendered site.

Images and other assets should live beneath `docs/assets` when the first one is
introduced, use meaningful filenames and alt text, and contain no private
device data. Do not add an image merely to break up prose; it should
teach something the words or a small table cannot show as clearly.

## Validate the change

Read the rendered Markdown as a new user, open every changed relative link and
check that commands still match `lexr <command> --help`. Then run:

```sh
git diff --check
go test ./internal/quality
mkdocs build --strict
```

The quality package checks project-wide prose conventions including British
English. The MkDocs configuration requires MkDocs 1.6 or newer and checks its
curated navigation, document links and heading anchors. A future publication
pipeline should consume these repository files without rewriting them. Report
any unrelated failure separately rather than weakening the documentation
contract to make a check pass.
