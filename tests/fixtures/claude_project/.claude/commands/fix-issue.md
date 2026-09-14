---
description: Fix a GitHub issue
argument-hint: "[issue-number]"
allowed-tools: Bash(gh:*), Read, Edit
model: haiku
---
Fix issue #$ARGUMENTS. Current status: !`gh issue view $ARGUMENTS`
