# CI/CD Pipeline Guide

This document describes the CI/CD pipeline for **OD-amrita**, a fullstack Go backend + Expo React Native frontend application.

## Overview

The pipeline runs on **GitHub Actions** and is triggered by:

- **Push to main/develop branches**
- **Pull requests to main/develop branches**
- **Weekly schedule** (Sundays at 2 AM UTC) for burn-in testing

## Pipeline Stages

### 1. Lint Stage

**Purpose**: Code quality checks

**Jobs**:
- Frontend linting: `npm run lint` (from `frontend/`)
- Backend formatting: `go fmt -s -l .`
- Backend vetting: `go vet ./...`

**Timeout**: 10 minutes

**Status**: 
- ✅ Required — must pass to proceed
- 🚫 Blocks PR merge if fails

### 2. Test Stage

**Purpose**: Unit and integration test execution

#### Backend Tests (Go)

- **Command**: `go test -v -coverprofile=coverage-*.out ./...`
- **Sharding**: 2 parallel shards
- **Timeout**: 30 minutes per shard
- **Artifacts**: Coverage files uploaded
- **Status**: ✅ Required — must pass to merge

#### Frontend Tests (Expo)

- **Status**: ⚠️ Placeholder — no test framework configured
- **Recommendation**: Set up Jest + React Native Testing Library
  - See: https://docs.expo.dev/testing/overview/
- **Next**: Update workflow with frontend test command after setup

### 3. Burn-In Stage

**Purpose**: Detect flaky tests by running them 10 times

- **Trigger**: PRs to main/develop + weekly cron schedule
- **Stack**: Go backend tests (deterministic, recommended for backend)
- **Iterations**: 10 consecutive runs
- **Exit condition**: Any failure exits with code 1 (blocks merge)
- **Artifacts**: Test output and failure traces

**Why Burn-In?**
Burn-in loops catch intermittent failures from race conditions, timing assumptions, and non-deterministic test behavior that single runs miss.

### 4. Report Stage

**Purpose**: Aggregate results and publish summary

- **Artifacts**: Downloads all shard results
- **Summary**: Posts to GitHub workflow summary
- **Status badges**: Reports pass/fail

## Configuration

### Environment Variables

```yaml
GO_VERSION: "1.25.5"  # Set in workflow env: block
```

### Caching

**Node.js / npm**:
- **Path**: `frontend/node_modules` (via `npm ci` with package-lock.json)
- **Key**: `npm-${{ hashFiles('frontend/package-lock.json') }}`
- **Hit rate**: 90%+ (saves ~3 minutes per run)

**Go Modules**:
- **Path**: `$GOPATH/pkg/mod` (managed by Go)
- **Key**: Automatic (go.mod and go.sum)
- **Hit rate**: 95%+ (saves ~1 minute per run)

## Running Locally

### Mirror CI environment locally

Use the provided helper scripts:

```bash
# Run tests exactly as CI does
bash scripts/ci-local.sh

# Run tests on changed files only (faster feedback)
bash scripts/test-changed.sh

# Run burn-in locally (10 iterations)
bash scripts/burn-in.sh
```

### Manual testing

**Backend**:
```bash
go test -v ./...
```

**Frontend**:
```bash
cd frontend
npm ci
npm run lint
# Add frontend tests here when framework is configured
```

## Understanding Test Results

### GitHub Actions Dashboard

1. Go to repository → **Actions** tab
2. Click workflow run name
3. View individual job logs and artifacts

### Artifact Downloads

**Test Results**:
- `test-results-[shard]` — JUnit/TAP output
- `coverage-backend-[shard]` — Coverage profile

**Burn-in Output** (if triggered):
- `burn-in-failures` — Flaky test traces and logs

## Troubleshooting

### Tests pass locally but fail in CI

**Common causes**:
- Environment differences (Node version, Go version)
- Timing-sensitive tests (race conditions)
- File path assumptions
- Network dependencies

**Debug**:
```bash
# Use ci-local.sh to match CI environment
bash scripts/ci-local.sh

# Run burn-in to detect flakiness
bash scripts/burn-in.sh

# Check logs in GitHub Actions UI for detailed failures
```

### Cache not working

**Symptoms**:
- `npm install` taking 3+ minutes
- Go modules redownloading

**Solutions**:
1. Verify lockfiles (`package-lock.json`, `go.sum`) are committed
2. Check cache key formula in workflow (should hash lockfile)
3. Manual cache clear: GitHub Actions → Caches → Delete

### Slow test execution

**If >45 minutes total**:
- Reduce burn-in iterations (from 10 to 5)
- Increase shard count (from 2 to 4)
- Run selective tests on PRs (changes-only mode)

**If <5 minutes total**:
- Increase shard count to better parallelize
- Add more comprehensive tests

## Secrets & Variables

### Required GitHub Secrets

None required for basic pipeline. Optional:

- `SLACK_WEBHOOK_URL` — For Slack notifications (see QUALITY_GATES.md)

### Environment Setup

**Node.js**: Detected from `.nvmrc` or defaults to Node 24 LTS

**Go**: Set in workflow as `GO_VERSION: "1.25.5"`

## Quality Gates

See [QUALITY_GATES.md](../.github/QUALITY_GATES.md) for:
- Minimum pass rates (P0/P1/P2)
- Coverage thresholds
- Failure conditions
- Merge requirements

## Next Steps

### Frontend Testing (Recommended)

1. Add Jest + React Native Testing Library
2. Create test files (`*.test.ts`, `*.test.tsx`)
3. Update workflow with test command:
   ```yaml
   - name: Run frontend tests
     working-directory: frontend
     run: npm test
   ```

### Slack Notifications (Optional)

Add to workflow in report job (see QUALITY_GATES.md).

### Codecov / Coverage Dashboard (Optional)

1. Connect to Codecov (codecov.io)
2. Add step to workflow:
   ```yaml
   - name: Upload coverage to Codecov
     uses: codecov/codecov-action@v3
     with:
       files: ./coverage-*.out
   ```

## Resources

- **GitHub Actions Docs**: https://docs.github.com/en/actions
- **Go Testing**: https://golang.org/doc/effective_go#testing
- **Expo Testing**: https://docs.expo.dev/testing/overview/
- **Quality Gates**: [QUALITY_GATES.md](../.github/QUALITY_GATES.md)
- **Workflow File**: [.github/workflows/test.yml](../.github/workflows/test.yml)

---

**Generated**: 2026-07-01  
**Stack**: Fullstack (Go + Expo)  
**Platform**: GitHub Actions  
**Status**: ✅ Active
