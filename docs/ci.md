# CI 工具安装与失败处理

`Test` workflow 中，`lint` 和 `go-test` 独立执行。原来的 `test` 检查名
保留为汇总门禁：两项都成功才通过，失败、跳过或取消均不能放行。
Web 和 Python 推理检查继续独立运行。

## golangci-lint 安装

- 版本由 `.github/workflows/test.yml` 的 `GOLANGCI_LINT_VERSION` 固定，
  升级时同步修改 `.pre-commit-config.yaml`。
- 缓存包含可执行文件本身，键包含操作系统、架构、Go 版本、lint 版本
  和安装脚本哈希，不使用模糊回退键。
- 每次恢复后检查 Go 构建元数据中的命令路径和模块版本，并实际运行
  `golangci-lint version`。无效缓存会触发重新安装。
- 冷启动通过 `proxy.golang.org` 构建固定版本，由 `sum.golang.org`
  校验模块。安装过程不再依赖 GitHub Release 资产下载。
- 安装最多尝试三次，间隔 5 秒、10 秒；整个 lint job 限时 20 分钟。
  安装先写入临时目录，验证成功后才替换正式文件并保存缓存。
- lint 检查本身不重试，也不忽略错误。工具缓存会在检查代码前保存，
  因此业务代码的 lint 错误不会导致下次重新构建工具。

这消除了原先每次运行都下载 lint Release 的故障点，但首次构建仍需要
Go 模块服务；Go 安装、Action 获取等也仍需要网络。如果模块服务持续
不可用，安装必须失败，不能跳过检查。如果精确键下的缓存损坏，脚本
会在本次运行重新构建；由于 Actions 缓存不可覆盖，应删除对应缓存，
使下一次成功运行能够重新保存。

## 验证与排查

安装逻辑的离线回归测试：

```sh
python3 -m unittest discover -s scripts/tests -v
```

使用与 CI 相同的 Go 版本执行真实冷启动和检查：

```sh
GOPROXY=https://proxy.golang.org GOSUMDB=sum.golang.org \
  bash scripts/install-ci-lint.sh v2.11.1 "$PWD/bin/ci-lint"
bin/ci-lint/golangci-lint config verify
bin/ci-lint/golangci-lint run --build-tags=ci
```

再次执行安装命令应显示 `Using cached golangci-lint`。安装阶段失败时
检查模块下载日志；`Run golangci-lint` 失败时处理实际诊断。
`go-test` 的结果可独立查看，最终仍需 `test` 汇总检查通过。
