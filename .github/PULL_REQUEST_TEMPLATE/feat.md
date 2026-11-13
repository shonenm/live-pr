<!-- The comments below are the guidance for each section. The same section has the same guidance in every template under .github/; change them together. -->

## Background and purpose

<!-- Before any detail, say what changes and in which situation. Name each target uniquely (path and symbol, command, config key, key binding), say who uses it and when (an interactive TUI session, the CLI, an agent, CI), and whether released behavior changes. Classify the prior state: an observed defect, a planned increment not built yet, a dependency or contract change not yet followed, or a new requirement. Call it a problem only for an observed defect, and say how it arose. Then state the purpose. A roadmap stage name alone is not a reason. -->

## Overview of the change

<!-- The behavior and structure after the change, as a user or caller sees it. Keep the method and its rationale for the next section. -->

## Key implementation points

<!-- Design decisions and why they were chosen. Point to the primary sources and existing code you actually opened (version and location) and why they apply here. Put the reasoning that supports important design claims under Evidence and verification. -->

## Evidence and verification

<!-- Short paragraphs: each claim, the explanation that supports it, what was done, and the concrete results or numbers. Split by claim with ### headings if needed. No bullet lists or field records, and no metadata such as claim type, status, commit SHA, or environment. Link the sources you actually opened, at the version and location you read, inside the explanation; a URL that resolves is not evidence by itself. State what was not verified and why; never write that something unread, unmeasured, or not run was confirmed. -->

## Behavior guaranteed by tests

<!-- Separate what this change observed once from what tests will keep guaranteeing, in the three parts below. -->

### Newly guaranteed

<!-- The behavior now guaranteed and the tests that guarantee it. If none, say none and why. -->

### No longer guaranteed

<!-- Guarantees removed or weakened, and why. If none, say none. -->

### Not guaranteed

<!-- Important behavior left without a guarantee. State the boundary even when no test changed. -->

## Impact

<!-- Only what applies: compatibility (CLI flags, config keys, key bindings, on-disk state and caches), anything GitHub shows that live-pr would stop showing, whether the fix also applies to 1.x, local CI trust (.live-pr.toml, .live-pr/, the attest gate), migration, security, performance, dependencies. If nothing applies, say why. A breaking change also needs `!` in the title. -->

## Screenshots

<!-- For a change in how the TUI looks or behaves on screen: a screenshot or short recording. Attach only media you actually captured; otherwise leave this empty. -->

---

## Provenance

<!-- Only what you can confirm, one line per part. Do not infer people from a GitHub login, OS user, or hostname. No session IDs or private records. -->

### Implemented by

<!-- Who implemented the change: a person, or an agent and the tool it ran in, and with whom. Later description updates do not replace this. -->

### Description written by

<!-- Who wrote or last updated this description, separately from who implemented the change. -->

## Related requests and plans

<!-- Only the requests, issues, and plans this relates to, in about one line (`Closes #123`). A roadmap stage (A1, A2) appears only here, with what the stage is and where it is described. -->

## Checklist

<!-- For a human to check after the pull request is opened. Leave every item unchecked when creating it, and do not add notes. -->

- [ ] The description is adequate for a reviewer who has not seen the code
- [ ] I can explain why every changed line is implemented the way it is
