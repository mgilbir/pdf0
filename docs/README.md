# pdf0 documentation

Start from the question you have.

| I want to… | Read |
|------------|------|
| Decide whether to use pdf0, or find an entry point | [../README.md](../README.md) |
| Understand how a PDF becomes a `Document`, and back | [architecture.md](architecture.md) |
| Pick a validator, or add a rule to one | [validators.md](validators.md) |
| Sign a PDF, or decide whether a signature can be trusted | [signing.md](signing.md) |
| Pull images out of a PDF, or fix a codec | [images.md](images.md) |
| Use the `cmd/pdf0` dev tool | [cli.md](cli.md) |
| Work out why something failed | [troubleshooting.md](troubleshooting.md) |
| Run the tests that a fresh clone skips | [testing.md](testing.md) |
| Contribute a change | [../CONTRIBUTING.md](../CONTRIBUTING.md) |
| Know why something was built the way it was | [adr/](adr/README.md) |
| Read a past findings report | [audits/](audits/README.md) |

The per-symbol API reference is the godoc: `go doc github.com/mgilbir/pdf0`, or
[pkg.go.dev](https://pkg.go.dev/github.com/mgilbir/pdf0).

## What each doc is for

**[architecture.md](architecture.md)** — explanation. The object model, the Read
pipeline and its recovery ladder, the Write pipeline. Read before changing the
parser or serializer.

**[validators.md](validators.md)** — reference plus explanation. Ten standards,
which entry point returns what, the PDF/A dispatch and executed-content model,
and where each rule lives.

**[signing.md](signing.md)** — how-to plus reference. Opens with which verdict to
read, because that is the question with a wrong answer that compiles.

**[images.md](images.md)** — reference plus explanation. The extraction API and
its memory contract, the codec dispatch, the resource budgets.

**[cli.md](cli.md)** — reference for `cmd/pdf0`, a small command-line front end
used mainly for poking at files during development. pdf0 is a library first; this
tool is not the supported surface and reaches only a fraction of the API. The doc
records every command, flag and exit code with real captured output, and what the
tool does not expose.

**[troubleshooting.md](troubleshooting.md)** — how-to, organised by symptom.

**[testing.md](testing.md)** — reference. The test tiers, the twelve external
datasets and how to fetch them, the fuzzers, and what CI does and does not check.

**[adr/](adr/README.md)** — explanation. Decisions that keep being
re-litigated, with the rejected alternative recorded.

**[audits/](audits/README.md)** — historical findings reports and the live plans
that work them off. Point-in-time snapshots, not a description of the current
code.
