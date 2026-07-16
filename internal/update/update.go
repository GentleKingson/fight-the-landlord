// Package update 提供客户端启动时的版本更新检测能力。
//
// 检测逻辑通过 GitHub Releases API 获取最新发布版本，与当前编译注入的版本号比较。检测过程使用较短的超时时间，且任何失败（无网络、超时、解析错误等）都不会影响客户端正常启动。
package update

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

const (
	Repo         = "palemoky/fight-the-landlord"
	checkTimeout = 2 * time.Second // 更新检测的最大耗时，避免拖慢客户端启动
)

// Result 表示一次更新检测的结果
type Result struct {
	CurrentVersion string // 当前客户端版本
	LatestVersion  string // 远端最新发布版本
	HasUpdate      bool   // 是否存在可用的新版本
	ReleaseURL     string // 最新版本的发布页地址
}

// release 对应 GitHub Releases API 返回的部分字段
type release struct {
	TagName    string `json:"tag_name"`
	HTMLURL    string `json:"html_url"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
}

// IsDevVersion 判断版本号是否为开发版（非正式发布），此类版本跳过更新检测
func IsDevVersion(version string) bool {
	v := strings.TrimSpace(version)
	return v == "" || v == "dev" || v == "unknown"
}

// Check 查询最新版本并与 currentVersion 比较
//
// 当存在可用更新时返回的 Result.HasUpdate 为 true。任何网络或解析错误都会以 error 返回，调用方应将其视为「无法检测」而非「需要更新」
func Check(ctx context.Context, currentVersion string) (*Result, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", Repo)
	return checkAt(ctx, currentVersion, url)
}

// checkAt 是 Check 的内部实现，允许注入自定义 URL 以便测试
func checkAt(ctx context.Context, currentVersion, url string) (*Result, error) {
	ctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("update: unexpected status %d", resp.StatusCode)
	}

	var rel release
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, err
	}
	if rel.TagName == "" {
		return nil, fmt.Errorf("update: empty tag_name in response")
	}

	return &Result{
		CurrentVersion: currentVersion,
		LatestVersion:  rel.TagName,
		HasUpdate:      CompareVersions(currentVersion, rel.TagName) < 0,
		ReleaseURL:     rel.HTMLURL,
	}, nil
}

// CompareVersions 比较两个语义化版本号（形如 v1.2.3）
//
// 返回值：a < b 时为负数，a == b 时为 0，a > b 时为正数。无法解析的版本按 v0.0.0 处理。
func CompareVersions(a, b string) int {
	return semver.Compare(comparableVersion(a), comparableVersion(b))
}

func comparableVersion(v string) string {
	v = strings.TrimSpace(v)
	if strings.HasPrefix(v, "V") {
		v = "v" + strings.TrimPrefix(v, "V")
	} else if !strings.HasPrefix(v, "v") {
		v = "v" + v
	}
	if !semver.IsValid(v) {
		return "v0.0.0"
	}
	return v
}
