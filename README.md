# Claude Usage Monitor for Windows

基于 [`egoist/mygo`](https://github.com/egoist/mygo)（纯 Go、零 CGO、Direct3D 11 原生 GPU UI，无需 WebView2）1:1 复刻 [`kittors/claude-usage-monitor`](https://github.com/kittors/claude-usage-monitor) 的 Windows 原生系统托盘应用。

## 核心特性

- **官方限额直读（不估算）**：直接调用与 Claude Code `/usage` 相同的 `GET https://api.anthropic.com/api/oauth/usage` 与 `/api/oauth/profile` 接口，显示 5 小时、每周全部模型与按模型限额及安全线（Pace Line），向下取整规则完全一致。
- **双模式 OAuth 凭据（兼容 Claude Code 与纯 Claude Desktop）**：
  - 自动读取 `%USERPROFILE%\.claude\.credentials.json`（兼容 `CLAUDE_CONFIG_DIR`），并使用与 Claude Code 相同的目录锁（`.oauth_refresh.lock` / `.storage-write.lock`）安全自动续期；
  - 仅读取配置目录（默认 `~/.claude` 或在「设置 › 数据」中自定义的 `.claude` 目录）中的 `.credentials.json` 凭据文件；无凭据时不主动请求 Claude 接口。
- **零请求缓存与防 429 调度**：监听 `%USERPROFILE%\.claude.json` 复用 `cachedUsageUtilization`（60 秒内零请求）；内置持久化令牌桶（容量 5、20 分钟回补 1 次、保留手动刷新配额、429 指数退避 15–60 分钟）与动态步长触发策略。
- **出口安全检测**：支持自定义 HTTP/SOCKS5 代理或自动读取 Windows 系统代理，请求前强制 IPv4 探测 `api.anthropic.com/cdn-cgi/trace`（在 CN/HK/MO 自动暂停），同时独立探测 IPv6 直连泄漏并显示警告。
- **本地日志增量索引与统计**：监听 `%USERPROFILE%\.claude\projects\**\*.jsonl`，增量解析、流式去重（`message.id + requestId`）、本地持久化缓存，展示本月周期 / 本周期 / 每日等价花费、14 天趋势柱状图、Token 构成、缓存命中率与节省金额、按模型拆分，以及近 5 分钟 / 1 小时输出速度（tok/s）。
- **Windows 原生交互**：系统托盘动态图标 + 左键托盘上方 Flyout 弹出面板 + 右键菜单 + 可选 Windows 专属置顶 Mini 悬浮条（还原 macOS 菜单栏多列数值与 Clawd 出口盾牌）。

## 构建与运行

需要 Go 1.27.3+（`GOTOOLCHAIN=auto` 会自动拉取）。

```powershell
# 运行单元测试与无头 UI 渲染测试
go test -v ./...

# 构建无控制台窗口的轻量单文件原生程序（不含 WebView2）
go build -trimpath -tags "mygo_nowebview mygo_noinspector" -ldflags "-H=windowsgui -s -w" -o build\ccmonitor.exe .\cmd\ccmonitor

# 正常启动（自动读取本机 Claude Code 凭据与日志）
.\build\ccmonitor.exe

# 演示模式启动（展示完整样例数据预览 1:1 界面）
.\build\ccmonitor.exe --demo
```
