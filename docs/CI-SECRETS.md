# CI/CD Secrets & Configuration Checklist

This checklist guides you through setting up secrets and required configuration for the OD-amrita CI/CD pipeline.

## Required Secrets

### ✅ No Blocking Secrets

The CI pipeline **does NOT require any secrets to run**. Basic pipeline execution (lint, test, burn-in, report) works immediately after pushing the workflow file.

## Optional Secrets

### Slack Notifications

**If enabled**, set up Slack webhook:

1. **Create Slack Webhook**:
   - Go to your Slack workspace → Settings → Apps & Integrations
   - Search for "Incoming Webhooks"
   - Create New Webhook URL
   - Copy the URL

2. **Add GitHub Secret**:
   - Go to repository → Settings → Secrets and Variables → Actions
   - Click "New repository secret"
   - Name: `SLACK_WEBHOOK_URL`
   - Paste webhook URL
   - Save

3. **Uncomment workflow step** (in `.github/workflows/test.yml`):
   - Uncomment the "Notify Slack on failure" step in the report job
   - Deploy the updated workflow

## Environment Variables

These are **set in the workflow file** (already configured):

| Variable | Value | Purpose |
|----------|-------|---------|
| `GO_VERSION` | `1.25.5` | Go runtime version |
| `NODE_VERSION` | `24` | Node.js version (frontend) |

To change these, edit `.github/workflows/test.yml` and update the `env:` block.

## GitHub Configuration

### 1. Branch Protection Rules (Recommended)

Enforce CI passing before merge:

1. Go to repository → Settings → Branches
2. Click "Add rule"
3. Branch name pattern: `main`
4. Enable: "Require status checks to pass before merging"
5. Select checks to require:
   - `lint` (lint job)
   - `test-backend` (backend tests)
   - `test-frontend` (frontend tests, when configured)
6. Save

### 2. Status Checks Configuration

Already auto-detected by GitHub Actions. Check:
- Repository → Actions → Workflows
- Verify "Test Pipeline" shows as active

### 3. Concurrency Settings

**Already configured** in workflow:
- Only 1 run per branch at a time
- New push cancels previous pending run
- Prevents race conditions in CI

## Checklist

### Pre-Deployment

- [ ] Workflow file committed: `.github/workflows/test.yml`
- [ ] Quality gates file committed: `.github/QUALITY_GATES.md`
- [ ] Documentation committed: `docs/CI.md`, `docs/CI-SECRETS.md`
- [ ] All files pushed to remote

### First CI Run

- [ ] Push to `main` or `develop` branch
- [ ] Go to Actions tab
- [ ] Verify workflow triggered
- [ ] Monitor logs for errors

**Expected results**:
- ✅ Lint passes
- ✅ Backend tests pass (2 shards)
- ✅ Frontend placeholder shows info message
- 🔄 Burn-in skipped (only on PR or schedule)
- ✅ Report generated

### Optional Enhancements

**Slack Notifications** (if desired):
- [ ] Create Slack webhook
- [ ] Add `SLACK_WEBHOOK_URL` secret
- [ ] Uncomment notification step
- [ ] Push updated workflow

**Coverage Dashboard** (optional):
- [ ] Sign up at codecov.io
- [ ] Connect to GitHub repo
- [ ] Add upload step to workflow
- [ ] Enable branch coverage reports

**Frontend Testing** (recommended):
- [ ] Set up Jest + React Native Testing Library
- [ ] Create test files
- [ ] Update workflow with test command
- [ ] Adjust quality gates for frontend tests

## Troubleshooting

### Workflow not triggering

**Check**:
1. Is file at `.github/workflows/test.yml`? (exact path required)
2. Has the file been pushed to remote?
3. Are you on `main` or `develop` branch?

**Fix**:
```bash
git add .github/workflows/test.yml
git commit -m "Add CI pipeline"
git push origin main
```

### Tests fail in CI but pass locally

**Debug**:
```bash
# Mirror CI environment
bash scripts/ci-local.sh

# Run burn-in to detect flakiness
bash scripts/burn-in.sh

# Check workflow logs for specific errors
# Go to Actions → failed run → job logs
```

### Secret not found error

**Ensure**:
- Secret name in workflow matches exactly (case-sensitive)
- Repository → Settings → Secrets shows the secret
- Secret was saved (click "Add secret" completes the action)

**Example**:
- Workflow references: `${{ secrets.SLACK_WEBHOOK_URL }}`
- GitHub secret must be named exactly: `SLACK_WEBHOOK_URL`

## Recommended Reading

- [GitHub Actions Secrets Docs](https://docs.github.com/en/actions/security-guides/encrypted-secrets)
- [Branch Protection](https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-protected-branches/about-protected-branches)
- [Status Checks API](https://docs.github.com/en/rest/commits/statuses)

---

**Last Updated**: 2026-07-01  
**Platform**: GitHub Actions  
**Project**: OD-amrita  

---

## Approval Sign-Off

When all items are configured:

**Configured by**: ________________  
**Date**: ________________  
**Notes**: ________________
