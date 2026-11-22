// Run: node --test scripts/tests/check-commits.test.mjs
// The pull request commit check (.github/workflows/commits.yml).
import { test } from 'node:test'
import assert from 'node:assert/strict'

import { problemWith, TYPES, MAX_SUBJECT } from '../../.github/scripts/check-commits.mjs'

test('conventional subjects pass, with scope, breaking mark and the squash PR number', () => {
  for (const type of TYPES) assert.equal(problemWith(`${type}: do a thing`), undefined, type)
  for (const s of ['fix(plan): route first (#6)', 'feat(install)!: one line', 'chore: release 0.2.0']) assert.equal(problemWith(s), undefined, s)
})

test('subjects GitHub writes pass', () => {
  for (const s of ['Revert "fix: something"', 'Merge pull request #12 from a/b', 'Merge branch main into x']) assert.equal(problemWith(s), undefined, s)
})

test('everything else is rejected', () => {
  for (const s of ['fixup! fix: x', 'wip: stuff', 'Fix: capitalised', 'fix:x', 'fix: ', 'feat(): empty scope', 'update readme', '']) {
    assert.match(problemWith(s) ?? '', /not a conventional commit/, JSON.stringify(s))
  }
})

test('the subject is at most 72 characters', () => {
  const at = 'fix: ' + 'x'.repeat(MAX_SUBJECT - 5)
  assert.equal(problemWith(at), undefined)
  assert.match(problemWith(at + 'x') ?? '', /longer than 72/)
})
