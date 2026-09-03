---
name: release
description: Cut a release
extensions:
  github-copilot:
    mode: agent
    model: gpt-5
    stemma.promptFile: release.prompt.md
---

1. Update `CHANGELOG.md`.
2. Tag the commit as `vX.Y.Z`.
3. Push the tag.
