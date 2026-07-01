# Quality Gates Configuration

## Test Execution Standards

### Minimum Pass Rates

| Test Class | Min Pass Rate | Severity | Action |
|-----------|--------------|----------|--------|
| **P0 (Critical)** | 100% | Blocking | Fail CI + Block PR merge |
| **P1 (High)** | ≥ 95% | Warning | Fail CI + Block PR merge |
| **P2 (Medium)** | ≥ 80% | Advisory | Warn in PR + Allow merge |
| **P3 (Low)** | ≥ 60% | Info | Report only |

### Current Quality Thresholds

#### Backend (Go Tests)
- ✅ All unit tests must pass (P0)
- ✅ All integration tests must pass (P1)
- 📊 Coverage requirement: ≥ 70% (advisory)
- 🔥 Burn-in loop: 10 iterations (flaky detection)

#### Frontend (Placeholder)
- ⚠️ No test framework configured yet
- 📋 After setup: Same thresholds as backend
- 📚 Recommended: Jest + React Native Testing Library
  - See: https://docs.expo.dev/testing/overview/

### CI Failure Conditions

The following events will **FAIL the CI pipeline** and **BLOCK** PR merges:

1. **Lint failures** (Go fmt, Go vet, npm lint)
2. **Backend test failures** (go test ./...)
3. **Burn-in flakiness detected** (any iteration failure)
4. **Coverage below threshold** (Go: <70%)

### Passing CI

A PR is only mergeable when:

✅ All lint checks pass  
✅ All backend tests pass  
✅ Burn-in completes successfully (if triggered)  
✅ Coverage ≥ 70% (advisory, but recommended)

---

## Notifications & Alerts

### GitHub UI Integration

The pipeline provides built-in notifications via:

- **PR Checks**: Show pass/fail status on PR
- **GitHub Actions**: Workflow run summary
- **Artifact Links**: Test results and coverage reports available in workflow artifacts

### Recommended: Slack Integration (Optional)

To add Slack notifications, add the following to `.github/workflows/test.yml` in the report job:

```yaml
  - name: Notify Slack on failure
    if: failure()
    uses: slackapi/slack-github-action@v1
    with:
      payload: |
        {
          "text": "❌ Test Pipeline Failed - ${{ github.server_url }}/${{ github.repository }}/actions/runs/${{ github.run_id }}",
          "blocks": [
            {
              "type": "section",
              "text": {
                "type": "mrkdwn",
                "text": "*Test Pipeline Failed*\n*Repository*: ${{ github.repository }}\n*Branch*: ${{ github.ref_name }}\n*Author*: ${{ github.actor }}"
              }
            }
          ]
        }
    env:
      SLACK_WEBHOOK_URL: ${{ secrets.SLACK_WEBHOOK_URL }}
```

Store the webhook URL as a GitHub Secret: `SLACK_WEBHOOK_URL`

---

## Burn-In Strategy

### Why Burn-In?

Burn-in loops detect flaky tests by running them multiple times under identical conditions. This catches:

- ✅ Race conditions in Go tests
- ✅ Timing-sensitive test assumptions
- ✅ Intermittent failures that slip through single runs

### Configuration

- **Frequency**: Every PR to main/develop + weekly schedule
- **Iterations**: 10x
- **Stack**: Go backend tests (deterministic; recommended default for backend)
- **Exit condition**: ANY iteration failure exits with code 1 (blocks merge)

### Interpreting Burn-In Results

| Outcome | Interpretation |
|---------|---|
| ✅ All 10 pass | Test is stable |
| ⚠️ 1+ fails | Flaky test detected - investigate timing/race conditions |
| ❌ Early exit | Critical failure - fix immediately |

---

## Contract Testing (Disabled)

This project does not currently use contract testing (PactJS). If you plan to add:

1. Configure `tea_use_pactjs_utils: true` in `_bmad/tea/config.yaml`
2. Re-run this workflow to generate contract-test stages
3. Set up Pact Broker (`PACT_BROKER_BASE_URL`, `PACT_BROKER_TOKEN`)
4. Add webhook authentication for provider verification

---

## Deployment Gates

Before promoting to staging/production:

1. ✅ All tests pass on main branch
2. ✅ No burn-in flakiness detected (if run)
3. ✅ Coverage maintained ≥ 70%
4. 📋 Manual approval (if required by team policy)

---

## Checklist for New Contributors

- [ ] Ensure local tests pass before pushing (`go test ./...`)
- [ ] Frontend tests should pass when test framework is added
- [ ] Check PR for lint warnings and fix before merge
- [ ] Monitor burn-in results if PR triggers weekly schedule
- [ ] Review coverage report in artifacts

---

## Next Steps

1. **Frontend Testing Setup** (recommended)
   - Add Jest + React Native Testing Library
   - Update pipeline with frontend test command
   - Adjust quality gates for frontend test requirements

2. **Slack Notifications** (optional)
   - Add webhook to GitHub Secrets
   - Uncomment Slack notification step in workflow

3. **Coverage Dashboard** (optional)
   - Set up Codecov or similar coverage tracking
   - Add coverage reports to PR checks

4. **Scheduled CI Burndown** (optional)
   - Run weekly burn-in to catch flakiness early
   - Already configured via cron schedule in workflow
