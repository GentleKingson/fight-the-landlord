# 发布验证

本 fork 的发布来源是 `GentleKingson/fight-the-landlord`，workflow 配置的未来发布
目标是：

```text
docker.io/gentlekingson/fight-the-landlord
docker.io/gentlekingson/fight-the-landlord-douzero
```

不要把上游 `palemoky` artifact 当成本 fork 的发布。首次 tag workflow 成功前，
fork 没有可供验证的正式 digest、SBOM 或签名；文档中的命令描述发布后必须执行的
验证流程，不构成“artifact 已发布”的声明。

## 固定 Digest

先检查 tag 的多架构 manifest，并记录输出中的顶层 digest：

```bash
docker buildx imagetools inspect docker.io/gentlekingson/fight-the-landlord:1.2.0
docker buildx imagetools inspect docker.io/gentlekingson/fight-the-landlord-douzero:1.2.0
```

生产 Compose 应把完整的不可变引用写入 `GAME_IMAGE_REF` 和
`DOUZERO_IMAGE_REF`，例如 `repository@sha256:...`。这两个变量会直接覆盖
`GAME_IMAGE`/`DOUZERO_IMAGE` 与 `IMAGE_TAG` 的组合；不要把 digest 填进 tag
变量，也不要只依赖 `latest`。

## Cosign

使用仍受支持且已修复已知验证漏洞的 Cosign 版本。把下面的 digest 替换为上一步
记录的值：

```bash
IMAGE=docker.io/gentlekingson/fight-the-landlord@sha256:<digest>

cosign verify "$IMAGE" \
  --certificate-oidc-issuer=https://token.actions.githubusercontent.com \
  --certificate-identity-regexp='^https://github\.com/GentleKingson/fight-the-landlord/\.github/workflows/release\.yml@refs/tags/v[0-9]+\.[0-9]+\.[0-9]+.*$'
```

对 DouZero digest 重复同一命令。验证必须同时成功匹配 digest、OIDC issuer 和本
仓库 `release.yml` 的 tag ref；只看到透明日志条目或只验证可变 tag 不够。

## SBOM 与 Provenance

BuildKit attestation 附着在远端镜像 index，无需先拉取整个镜像。发布 workflow
构建 `linux/amd64` 与 `linux/arm64`，必须分别提取并 fail-closed 检查，不能把多平台
map 当成单个平台文档：

```bash
set -euo pipefail
for platform in linux/amd64 linux/arm64; do
  output_name="${platform//\//-}"
  docker buildx imagetools inspect "$IMAGE" \
    --format "{{ json (index .SBOM \"$platform\").SPDX }}" \
    > "fight-landlord-${output_name}.spdx.json"
  docker buildx imagetools inspect "$IMAGE" \
    --format "{{ json (index .Provenance \"$platform\").SLSA }}" \
    > "fight-landlord-${output_name}.provenance.json"

  jq -e 'type == "object" and (.SPDXID | type == "string") and (.packages | type == "array")' \
    "fight-landlord-${output_name}.spdx.json" >/dev/null
  jq -e 'type == "object" and (.buildType | type == "string") and
    (.materials | type == "array") and (.invocation | type == "object")' \
    "fight-landlord-${output_name}.provenance.json" >/dev/null
done
```

逐平台确认 provenance 的 source repository/revision（若字段缺失则阻塞发布）、
构建参数和部署审批的 commit 一致，并确认正在部署的平台 manifest 受已签名的顶层
digest 约束。用对应平台 SBOM 扫描；任何 `null`、空对象或无法关联 commit 的输出都
不能作为发布证据。Docker 的
[SBOM](https://docs.docker.com/build/metadata/attestations/sbom/) 与
[provenance](https://docs.docker.com/build/metadata/attestations/slsa-provenance/)
文档描述了当前 inspect 格式。

### DouZero 模型材料

DouZero 构建从 Hugging Face commit
`57b3914046c2a0877016b8b8830fd07cf5b0ba08` 下载三个 ONNX 模型，源代码在
`douzero/model_assets.py` 固定每个文件的 SHA-256；已存在文件和新下载文件都必须
校验成功，镜像层才会生成。`scripts/check-compose-security.sh` 会拒绝
`resolve/main`/`resolve/master` 和缺失的 digest。

BuildKit provenance 不保证把 Dockerfile 内的 HTTP 下载列成独立 material，SBOM
也不等同于逐文件 hash 清单。因此验证 DouZero 时还必须审查发布 commit 中的固定
revision/digest，并确认构建日志出现三个模型的校验成功；不要只依赖 attestation
里是否出现 Hugging Face URL。

当前 Dockerfile 没有声明 `BUILDKIT_SBOM_SCAN_STAGE=true` 或 context scanning；按
BuildKit 默认语义，发布 SBOM 主要是最终运行阶段清单，不包含完整的 Go/npm/Python
构建依赖。它不能替代 `go.sum`、`package-lock.json`、`uv.lock` 与依赖扫描，也不得
被描述为完整源码供应链 BOM。

## 客户端二进制

GitHub Release 为每个二进制附带 `.sha256`。安装脚本下载失败、checksum 格式
错误或 hash 不匹配时都会停止：

```bash
sha256sum --check fight-the-landlord-linux-amd64.sha256
# macOS
shasum -a 256 -c fight-the-landlord-darwin-arm64.sha256
```

当前 workflow 没有为 Release 二进制生成 cosign 签名，因此 SHA-256 只能检测
下载内容与 Release 附件是否一致，不能单独证明发布者身份。需要更强策略的部署者
应先核验 GitHub tag/workflow，再把 hash 固定到自己的发布清单。

README 中从 `main` 获取并直接执行安装脚本的命令，本身仍信任可变分支、GitHub
传输和当时的脚本内容；附件 checksum 不能消除这段 bootstrap 信任。高保证环境应
从已审查的 tag/commit 获取安装脚本，先检查脚本与 Release 元数据，再执行安装。

## 升级与回滚

1. 在隔离环境验证签名、SBOM、provenance、`/version`、浏览器重连和容量 smoke。
2. 先部署兼容服务和 Web 资源，再提高 `SERVER_MIN_CLIENT_VERSION`。
3. 保存上一个已验证 digest 和对应配置。
4. 回滚时同时恢复镜像 digest、最低客户端版本和相关代理配置。

回滚不会恢复内存中的 Room/Matcher 所有权。当前单实例重启会丢失活跃房间，不能
把镜像回滚当作无中断故障转移。

跨 HttpOnly 迁移边界时，旧 localStorage token 只会被删除，不会转换成 Cookie；
升级后缺少新 Cookie 的浏览器可能获得新身份。回滚到旧 Web/服务端也不能读取
HttpOnly Cookie，可能再次要求重新建立身份。CLI/TUI 的显式 token 字段保持协议
兼容，但进程重启同样不会保留它们对应的内存会话。
