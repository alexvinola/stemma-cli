---
title: Asset conventions
priority: should
enabled: true
activation:
  type: path-scoped
  include:
    - app/**/*.css
    - app/**/*.scss
    - packages/**/*.css
    - packages/**/*.scss
extensions:
  claude:
    description: Asset conventions
    stemma.ruleFile: assets.md
---

Design tokens come from the theme package; never hard-code a hex colour.
