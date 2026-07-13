#!/usr/bin/env bash

# G01 只读基线复核：不启动应用、不加载环境文件、不连接数据库。
set -u

EXPECTED_BRANCH="feat/glm-5.2-v1.1"
EXPECTED_BASE_COMMIT="97762502f25e5e1c84228d13da2af72f7134e276"
REPO_ROOT="$(git rev-parse --show-toplevel 2>/dev/null)" || {
  echo "ERROR: 请在 atmApi Git worktree 内执行。" >&2
  exit 2
}
TIMESTAMP="$(date +%Y%m%d-%H%M%S)"
RESULT_DIR="${G01_RESULT_DIR:-/tmp/atmapi-g01-review-${TIMESTAMP}}"
mkdir -p "$RESULT_DIR"
exec > >(tee "$RESULT_DIR/verify.log") 2>&1

cd "$REPO_ROOT" || exit 2

echo "G01 Linux verification"
echo "result_dir=$RESULT_DIR"
echo "repo_root=$REPO_ROOT"
echo "branch=$(git branch --show-current)"
echo "head=$(git rev-parse HEAD)"
echo "expected_branch=$EXPECTED_BRANCH"
echo "expected_base_commit=$EXPECTED_BASE_COMMIT"

if [ "$(git branch --show-current)" = "$EXPECTED_BRANCH" ]; then
  echo "branch_check=PASS"
else
  echo "branch_check=FAIL"
fi

if git merge-base --is-ancestor "$EXPECTED_BASE_COMMIT" HEAD; then
  echo "base_commit_check=PASS"
else
  echo "base_commit_check=FAIL"
fi

if git diff --quiet && git diff --cached --quiet; then
  echo "tracked_worktree_check=PASS"
else
  echo "tracked_worktree_check=FAIL"
fi

echo
echo "[toolchain]"
go version || true
if command -v mariadb >/dev/null 2>&1; then
  mariadb --version || true
elif command -v mysql >/dev/null 2>&1; then
  mysql --version || true
else
  echo "mariadb_client=NOT_FOUND"
fi

echo
echo "[port 13300]"
if command -v ss >/dev/null 2>&1 && ss -ltn | grep -Eq '[:.]13300[[:space:]]'; then
  echo "port_13300=IN_USE"
else
  echo "port_13300=AVAILABLE_OR_SS_UNAVAILABLE"
fi

echo
echo "[go mod verify]"
go mod verify
MOD_VERIFY_RC=$?
echo "go_mod_verify_rc=$MOD_VERIFY_RC"

echo
echo "[go test ./...]"
go test ./...
GO_TEST_RC=$?
echo "go_test_all_rc=$GO_TEST_RC"

echo
echo "[go test -vet=off ./internal/...]"
go test -vet=off ./internal/...
INTERNAL_TEST_RC=$?
echo "go_test_internal_rc=$INTERNAL_TEST_RC"

echo
echo "[CGO_ENABLED=0 go build]"
CGO_ENABLED=0 go build -o "$RESULT_DIR/atmapi-linux-baseline" ./main.go
BUILD_RC=$?
echo "go_build_rc=$BUILD_RC"
if [ "$BUILD_RC" -eq 0 ]; then
  sha256sum "$RESULT_DIR/atmapi-linux-baseline" || true
fi

echo
echo "[summary]"
echo "go_mod_verify_rc=$MOD_VERIFY_RC"
echo "go_test_all_rc=$GO_TEST_RC (基线预计非零，必须核对是否仅为报告中的既有失败)"
echo "go_test_internal_rc=$INTERNAL_TEST_RC"
echo "go_build_rc=$BUILD_RC"
echo "application_started=NO"
echo "database_connected=NO"
echo "review_log=$RESULT_DIR/verify.log"

if [ "$MOD_VERIFY_RC" -eq 0 ] && [ "$INTERNAL_TEST_RC" -eq 0 ] && [ "$BUILD_RC" -eq 0 ]; then
  exit 0
fi
exit 1
