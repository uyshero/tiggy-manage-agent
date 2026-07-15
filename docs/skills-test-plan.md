# Skills 模块测试与验收

## 测试目标

验证 Skills registry、精确版本解析、离线 `inputs_schema`、TMA 工具调用、审批恢复、Organization 内部 Marketplace、GitHub 显式来源、文本/二进制 package assets、object refs、SBOM、provenance 和 Session config pinning。

## 自动化测试

### 全仓测试

```bash
go test ./...
git diff --check
```

### Skills 重点包

```bash
go test ./internal/skillmarketplace \
  ./internal/skills \
  ./internal/tools \
  ./internal/httpapi \
  ./internal/agentruntime \
  ./internal/runner
```

覆盖内容：

- canonical schema、legacy compatibility、renderer 和 budget degradation。
- `inputs_schema` Draft 2020-12 离线编译、本地 `$ref`、结构限额、Secret 注解拒绝和脱敏 validation error。
- version 发布、Marketplace 安装、Enable 与 Runtime Resolve 的精确版本 inputs 校验；非法 Enable 不得创建 Agent config version。
- `SKILL.md` GitHub/Artifact YAML front matter 到 version manifest 的 `inputs_schema` 保留。
- asset bundle round-trip、重复/越界路径拒绝和 asset index 渲染。
- GitHub token header、code/repository discovery、frontmatter 和 Contents API。
- GitHub 网络错误/429/5xx 有界重试、`Retry-After` 上限和 404 不重试。
- 引用闭包递归、script 标记、外部/越界引用跳过，以及显式引用 binary allowlist。
- 文本 100000 bytes、二进制 512 KiB、package 4 MiB 限额和 raw-byte package digest。
- `tma.skill.binary-scan.v1` 对 Base64、size/digest、扩展/MIME、executable magic、EICAR、PDF/Office active marker 的检查。
- `tma.skill.sbom.v1` component、package digest 和 binary object ref 关联。
- Preview 不写 object store/object ref；Install 上传后只持久化 object ref，不暴露 Base64。
- binary `skills.read_asset` forbidden；扫描阻断和安装失败均不留下 registry/object/ref 部分状态。
- 大小写不一致文档引用通过目录清单解析真实路径，不直接请求错误大小写路径。
- `skills.discover` 默认内部 Catalog 且不调用 GitHub；显式 `provider=github` 保持远程发现兼容。
- 内部 Catalog 同 Organization 跨 Workspace 可见、跨 Organization 不可见，并只返回 published 精确版本。
- `skills.search/inspect/discover/preview/read_asset/install/enable` manifest 与执行。
- preview 首次安装、无变化、升级 diff 和 blocked 状态；验证只读且 asset body 脱敏。
- Marketplace owner/repository allowlist、commit SHA pin、license allow/deny/必填和 deny 优先级。
- source policy 在 fetch 前阻止，license policy 在 fetch 后阻止；install 返回 forbidden 且 registry 不写入。
- package digest 稳定性、Ed25519 attestation 验证、篡改检测、missing/untrusted/invalid 状态。
- static scan rule/severity/阈值和 finding 数量上限；preview/install 使用同一 security policy。
- install/enable write-risk intervention，未审批时 service 调用次数为 0。
- workspace scope、重复安装、升级、Agent config version 和 Session pinning。
- Workbench typed controls、默认值、可选字段省略、数字/JSON 转换和逐字段客户端错误。

### Postgres 集成

```bash
make db-up
make migrate-up
TMA_RUN_POSTGRES_TESTS=1 \
TMA_DATABASE_URL='postgres://tma:tma@localhost:5432/tma?sslmode=disable' \
go test ./internal/managedagents \
  -run 'TestPostgres(SkillRegistryVersionsAndUsage|MarketplacePolicyVersionsAndPrecedence)' -count=1
```

覆盖内容：

- skill/version 顺序编号与 checksum。
- source type/repository/path 和 ref/revision/source URL 往返。
- 精确 binding 校验、归档后禁止发布、usage upsert 与查询。
- organization/workspace policy 创建、不可变版本、active scope 唯一性、优先级和归档回退。

## 手工对话验收

### Inline install 与 enable

1. 创建独立 Environment 和 Session。
2. 设置 `intervention_mode=request_approval`。
3. 要求 TMA 先 search，不存在时 install inline skill，再 enable。
4. 验证 install 与 enable 分别产生 pending intervention。
5. 批准后检查 tool result、Agent config version 和 `requires_session_upgrade=true`。
6. 升级 Session，发送不包含测试 marker 的请求。
7. 验证 `runtime.skills_resolved`、marker 回复和 resolved usage。

2026-07-13 结果：通过，验收 Session `sesn_000167` 已归档。

### GitHub discover 与 install

测试来源：

```text
repository: anthropics/skills
ref: main
path: skills/pdf/SKILL.md
```

验收点：

- `skills.discover` 无 token 时返回 repository search 和 `verified=false`。
- `skills.install` 在远程 fetch 和 registry 写入前等待审批。
- 批准后 content、checksum、repository/path/ref/revision/source URL 完整。
- 不调用 enable 时 Agent config version 不变化。

2026-07-13 结果：通过，验收 Session `sesn_000169` 和 skill 已归档。

### Package assets

1. 使用包含 `REFERENCE.md`、嵌套文档和 `scripts/*.py` 引用的测试 package。
2. 验证安装 version 的 assets 是标准 bundle，文件路径相对 package root。
3. 验证 install/inspect tool result 只包含 asset index，不包含正文。
4. 调用 `skills.read_asset` 读取文档和脚本文本。
5. 验证脚本返回 `executable=true`，但不会自动执行。
6. 验证 `../`、绝对路径、外部 URL、二进制、超数量/深度/大小限制。

2026-07-13 结果：通过，验收 Session `sesn_000174` 和 skill `skl_000015` 已归档。

- 安装 `anthropics/skills@main:skills/pdf/SKILL.md` version 1，revision 为 `d3e046a5ae107a6cb23cfb16c219837094ab35d3`。
- 持久化 9 个 assets、总计 49094 bytes；包含 `LICENSE.txt`、`forms.md`、`reference.md` 和 6 个 `executable=true` Python 脚本。
- install tool result 只包含 asset index，不包含 asset body；`skills.read_asset(reference.md)` 成功。
- Session 中 `skills.enable` 调用数为 0，默认 Agent config version 3 的 `skills_json` 仍为 `{"enabled": []}`。
- Session `sesn_000171` 至 `sesn_000173` 的失败回归确认：解析或 GitHub fetch 失败时，不创建目标 identifier 的 registry 记录。

### Package preview

1. 对未安装 identifier 调用 `skills.preview`，验证 `install_state=new_install`，且 registry 记录数不变。
2. 验证 source、revision、license、warnings、content bytes 和 asset index 完整，响应不包含 asset body。
3. 对已安装且内容相同 package 验证 `install_state=unchanged` 和 existing version。
4. 修改主内容及 assets，验证 `install_state=upgrade`、`content_changed` 和 added/removed/changed 路径。
5. 验证归档 skill 或 source provenance 不一致返回 `blocked` 和 `block_reason`。
6. 在 `request_approval` Session 中确认 preview 直接执行，不产生 pending intervention，也不创建 skill/version。

2026-07-13 结果：通过，验收 Session `sesn_000177` 已归档。

### 标准 SKILL.md 文件包存储

1. 未配置 package repository 时发布 legacy version，确认 `package_format=legacy_db`，旧调用链不受影响。
2. 配置 localfs 后调用 backfill，确认生成 `SKILL.md`、引用文本文件和 `.tma/package.zip`，并写入 package root/checksum/object refs。
3. 修改数据库 `content_text` 为不同内容，重新读取 version，确认对象存储 `SKILL.md` 优先。
4. 重复 backfill，确认 `migrated=0`，不创建重复 package file rows。
5. 新发布版本时模拟 object PUT 或数据库失败，确认新对象被清理、既有 binary object ref 保留。
6. 下载 package，确认 `application/zip`、安全附件文件名、package checksum header 和 ZIP 内标准相对路径。
7. active Skill 的 package refs 不进入 GC；归档且超过 retention 的 package files 可删除并写 tombstone。
8. 在 S3-compatible localhost 后端重复发布、回填和下载，确认无公网依赖。

2026-07-13 自动化结果：

- `TestRepositoryStoresStandardDeterministicPackage`、reserved path 回归通过。
- `TestDownloadSkillPackage` 与 legacy 404 回归通过。
- `TestPostgresSkillPackageStorageLocalFSE2E` 在真实 PostgreSQL/localfs 通过；legacy version 回填 1 次、重复回填 0 次，文件索引为 `SKILL.md`、`references/guide.md` 和 archive 共 3 条。
- 数据库正文被改为 stale 后，registry 返回内容仍与 object storage 原始 `SKILL.md` 一致。
- localhost S3 实测回填 4/4 legacy versions、26 条 package file refs；下载抽样 ZIP 为 260 bytes，仅含原始 `SKILL.md`，checksum header 正确。
- localhost S3 新发布 v1 生成 3 个 objects；Workbench 桌面/移动布局无溢出；归档到期后 GC preview=3、deleted=3、failed=0、429 bytes。
- `go test ./...` 通过。完整 `make test-postgres` 中 package/Skills 用例通过，但既有 `TestPostgresStoreConcurrentSubagentStartsDoNotExceedWorkspaceActiveLimit` 在共享数据库复跑仍失败（预期 5、实际 20）；该失败属于 Subagent 并发限额路径，未修改或掩盖。

- 预览 `anthropics/skills@main:skills/pdf/SKILL.md` 返回 `new_install`、frontmatter license、revision、9 个 assets 和 49094 bytes。
- preview tool result 的 asset body 字段数为 0，intervention、install 和 enable 调用数均为 0。
- identifier `tma-github-preview-e2e-20260713` 在预览前后 registry 记录数均为 0。
- Session `sesn_000176` 覆盖匿名 GitHub rate limit 403：工具失败但不写 registry；配置认证 token 后重试通过。

### Marketplace policy

1. 配置仅允许指定 owner/repository，验证未命中 source 的 preview 返回 `blocked`，且 GitHub fetch 调用数为 0。
2. 开启 `TMA_SKILLS_GITHUB_REQUIRE_COMMIT_SHA`，验证 branch/tag ref 被阻止，完整 40 字符 SHA 通过。
3. 配置 license allow/deny/required，验证 deny 优先、缺失 license 和不在 allowlist 的 license 均被阻止。
4. 验证 license token 边界，`Limited` 不得误命中 `MIT`。
5. 对 policy blocked source 调用 install，验证返回 forbidden，skill/version 数量不变。
6. 在真实 `request_approval` Session 中验证 preview policy decision；批准 install 后仍由服务端 policy 强制拒绝。

2026-07-13 结果：通过，验收 Session `sesn_000179` 已归档。

- 临时策略允许 `anthropics` owner、要求 license，并 deny `Proprietary`。
- preview 返回 repository check passed、license check failed、`policy.allowed=false` 和 `install_state=blocked`，intervention 数为 0。
- install approval 通过后仍返回 forbidden；identifier `tma-github-policy-e2e-20260713` registry 记录数保持为 0。
- 自动化测试另覆盖 source policy 在 fetch 前阻止、完整 commit SHA 通过及 license token 边界。

### Versioned policy and revision pin

1. 创建 organization policy 后验证 workspace 继承；创建 workspace policy 后验证覆盖 organization。
2. 发布 version 2，验证 version 1 不变、current version 更新且 checksum 改变。
3. preview 验证 `policy_source/id/version/revision` 完整。
4. 持久化 policy 下 install 缺少 pin 返回 conflict。
5. preview 后发布新 policy version，批准携带旧 revision 的 install，验证 conflict 且 registry 不写入。
6. 重新 preview 并使用当前 pin，验证 install 成功和 operator audit 完整。
7. 归档 workspace policy，验证自动回退 organization；归档所有持久化 policy 后回退 Server env。

2026-07-13 结果：通过，验收 Session `sesn_000183`、skill `skl_000024` 和 policy `smpol_000004` 已归档。

- v1 preview、v2 发布、stale v1 pin conflict、v2 re-preview 和 pinned install 全链路通过。
- stale pin 拒绝发生在 GitHub fetch/registry 写入前；成功 install audit 记录 package revision 和 policy v2 checksum。

### Package security

1. 生成 Ed25519 keypair 和 attestation，验证正确签名返回 `verified`。
2. 修改主文件或任一 asset，验证 digest 变化和签名失效。
3. 验证 required attestation 对 missing/untrusted/invalid 状态的处理；invalid attestation 默认也阻止。
4. 注入 instruction override、credential exfiltration、pipe shell、approval bypass 等文本，验证 finding 路径、行号和 severity。
5. 配置 scan threshold，验证达到阈值的 preview blocked、直接 install forbidden 且 registry 不写入。
6. 验证 preview response 与 operator audit 包含 security report，install 会独立重新计算。

2026-07-13 结果：通过，验收 Session `sesn_000186`、`sesn_000187` 和临时 policy `smpol_000008` 已归档。

- 默认 policy 对无 attestation、0 findings package 只报告不阻止。
- required attestation policy 对同一 package 返回 blocked；扫描 10 个文件且 registry 记录数保持为 0。

### Controlled binary assets

1. 使用 `SKILL.md` 明确引用一个小型 PNG/PDF，验证 closure 只抓取引用文件，并返回 binary、检测 MIME、size 和 SHA-256。
2. 调用 Preview，验证 `binary_files`、强制 `binary_scan` check、`tma.skill.sbom.v1` 和 asset index 完整，object store PUT/object ref/registry 均为 0。
3. 调用 Install，验证服务端重新 fetch/scan，上传一次对象，并在 version bundle 与 SBOM component 中只保存 `object_ref_id`，不存在 `content_base64`。
4. 使用 Session 作用域 download endpoint 下载并比对 SHA-256；调用 `skills.read_asset` 读取同 binary 必须返回 forbidden。
5. 分别注入 EICAR、错误扩展/MIME 和 executable magic，验证 Preview blocked、Install forbidden，且对象存储和 registry 零写入。
6. 制造上传后 package/version 发布失败，验证对象和 object ref 被清理。
7. 在桌面和移动 viewport 检查二进制数量、SBOM、MIME、扫描状态、digest 与受控下载链接；不得自动请求或内联 asset body。

2026-07-13 自动化结果：通过。

- `TestSkillsToolServicePersistsControlledBinaryAssets` 覆盖 Preview、Install、object ref、Base64 脱敏和 binary read forbidden。
- `TestSkillsToolServiceBlocksUnsafeBinaryAssetsBeforeWrites` 覆盖 EICAR 和 MIME 伪装的强制零写入阻断。
- `TestSkillsToolServiceCleansBinaryUploadWhenInstallFails` 覆盖上传成功后发布前失败的对象/ref 回滚。
- GitHub/security/bundle 聚焦测试与 Vite production build 通过。
- 真实来源 `anthropics/skills@main:skills/theme-factory/SKILL.md` closure 包含 `LICENSE.txt` 和 `theme-showcase.pdf`；PDF 124310 bytes、MIME `application/pdf`、SHA-256 `3e126eca9fe99088051f7cb984c97cedb31c7d9e09ce0ba5d61bd01e70a0d253`。
- Preview 返回 binary scan passed、0 findings 和 3-component SBOM，且 Skill/object ref 前后均为 0。
- Install 创建临时 `skl_000033`/`obj_000258`；数据库原始 bundle 无 Base64，binary 与 SBOM object ref 一致，代理下载 SHA-256 完全一致。
- 真实 PDF 首轮暴露 stream 随机 `/js` 误报和 Unicode lower 偏移 panic；新增 stream 排除、PDF Name token、ASCII-only fold 回归后真实 Preview 通过。
- Workbench 在 1280x720 和 390x844 验证 binary/SBOM/asset metadata，无 document/preview/search 横向溢出、自动 binary embed、Base64 字段或 console warning/error。
- 验收结束后临时 Skill、version、object 和 object ref 已清理，代理下载返回 404。

### External binary scanner（未来开发，当前禁用）

1. 验证默认 `builtin` 不构造外部 client；当前生产工厂对 `clamav_http` 固定返回 deferred，并且网络请求数为 0。
2. 验证 POST body、MIME、asset path、SHA-256 和 Bearer header；token 只从 `TOKEN_ENV` 引用解析。
3. 覆盖同步 passed、202 pending 轮询、503/429 重试、blocked、error、timeout、超大/无效 JSON response。
4. Preview 验证 `external_scan` 为空且无外部请求；Install 才执行外部扫描。
5. 外部 passed 后验证 asset/object metadata 的 provider/version、operator audit 和 Prometheus counter/duration。
6. 外部 blocked/error 验证 object store PUT、object ref、skill/version 均为 0；Policy `binary_scan` 强制失败。
7. Workbench 已安装详情展示 scanner provenance，桌面/移动无溢出且不加载 binary body。

2026-07-13 结果：通过。

当前发布状态：历史 HTTP contract 和 provenance 测试保留，但 `NewBinaryScanner` 不再返回 ClamAV client；运行时只能使用 builtin。以下结果是关闭生产入口前完成的开发验证，不代表当前可配置能力。

- `TestClamAVHTTPScannerReturnsSynchronousVerdict`、`PollsPendingVerdict`、`RetriesTransientFailure` 和 `TimeoutFailsClosed` 覆盖 HTTP contract、重试与超时。
- `TestApplyExternalBinaryScannerBlocksMalwareAndErrors` 覆盖外部 finding 与 Policy fail closed。
- `TestSkillsToolServicePersistsExternalBinaryScanProvenance` 覆盖 passed upload、bundle/object metadata 和 `skills.binary_scan` audit。
- `TestSkillsToolServiceExternalBinaryScanFailsBeforeWrites` 覆盖 malware/error 的 registry/object 零写入。
- 真实 fixture + GitHub PDF Install 完成 POST 202 + GET poll，scanner `ClamAV fixture 1.0`、attempts 2；下载 digest、audit、metrics 和 object metadata 一致。
- EICAR fixture verdict 为 blocked/`Eicar-Signature`；Workbench 在 1280x720、390x844 显示 `clamav_http · ClamAV fixture 1.0`，无溢出和 console warning/error。
- 临时 `skl_000036`、`obj_000259` 及 registry/object 数据已清理。

### Management API and UI

1. 验证 Marketplace discover/preview/install/enable 管理 API 需要 control auth。
2. 使用 stub GitHub package 完成 discover、Preview、安全摘要、安装和 Agent 启用 HTTP 生命周期。
3. 验证 install/enable 写入 control principal operator audit，Preview policy/security 仍由共享 service 生成。
4. 构建 Workbench，确认生产 bundle 包含 Skills 管理、Package Security 和 Marketplace Policy 三个界面入口。
5. 在桌面和移动 viewport 验证已安装、Marketplace、Policy 页无横向溢出、控件遮挡或文本重叠。
6. 使用真实 GitHub package 执行只读 Preview，确认页面展示 digest、Attestation、扫描文件数、Policy checks 和 package diff，且不写 registry。

2026-07-13 结果：通过。

- `TestSkillsMarketplaceHTTPDiscoverPreviewInstallAndEnable` 覆盖 Control Auth、discover、Preview、安全摘要、install、enable 和 operator audit。
- Vite production build 与 Go 静态资源测试通过；bundle 包含 Package Security 和 Marketplace Policy 管理界面。
- 浏览器在 `1280x720` 与 `390x844` 验证三视图；页面、主容器、表单和 Preview 面板均无横向溢出，控制台无 error/warning。
- UI 对 `anthropics/skills@main:skills/pdf/SKILL.md` 完成真实只读 Preview：digest `30aa13e4ecf89f337eadcdd92e34b96594b96df53fe61d6c0183b867eda21f89`、扫描 10 个文件、0 findings、Attestation missing、Policy allowed；registry 记录数保持为 0。

### Offline inputs_schema and typed Workbench inputs

1. 发布包含 required enum、integer range、optional boolean、textarea 和 array/object 字段的 version，确认 manifest 原样冻结。
2. 分别提交远程 `$ref`、`$id`、`$dynamicRef`、缺少 `additionalProperties: false`、Secret 注解和结构超限 Schema，确认返回 `400` 且不增加 version 数量。
3. 使用非法 enum/range/additional property 调用 Enable，确认错误不包含输入值，Agent `current_config_version` 不变化。
4. 使用合法输入 Enable，确认 binding 固定精确 version，数字和布尔值保持 JSON 类型，未填写可选字段不存在。
5. 直接调用 Runtime Resolve，验证旧配置中不合法 inputs 无法绕过 Enable 边界，合法 inputs 正常进入渲染上下文。
6. 从 GitHub fixture 与当前 Session Artifact ZIP 安装带 YAML `inputs_schema` 的 `SKILL.md`，确认 version manifest 与导出 package 均保留契约且不触发任意网络读取。
7. Workbench 验证 enum/select、boolean/checkbox、number、text/textarea 和 JSON fallback；客户端 required/range/JSON 错误不发送请求。
8. 在 `1280x720` 与 `390x844` 检查版本详情、长 title/description、错误文本、按钮和 JSON textarea，无横向溢出、遮挡或布局跳动。

2026-07-14 结果：通过。

- `go test ./internal/skills ./internal/skillmarketplace -count=1` 通过。
- `go test ./internal/httpapi -run 'Skill|Skills|UpgradeSessionAgentConfig' -count=1` 通过。
- `go test ./internal/tools ./internal/runner ./internal/agentruntime -count=1` 通过。
- `npm --prefix web-app test` 通过，共 58 个 tests，其中 5 个覆盖 typed Skill inputs。
- `npm --prefix web-app run build` 通过。
- 真实非法 Enable 返回 `400` 且 Agent config version 不变；合法 Workbench Enable 发布精确 v1 binding，Resolve Preview 为 `resolved` 并渲染同一 typed inputs。
- `1280x720` 与 `390x844` 几何检查均无横向溢出或 viewport 外控件；最终 rebuild 后的移动按钮规则已进入生成 CSS。浏览器 URL 策略阻止最后一次 reload，因此最终 bundle 未重复截图。
- 临时 Agent 已恢复空 binding，Skill/Session 已归档，不可变 package objects 保留。

### Binary asset retention and GC

1. 创建 organization policy，验证 workspace 继承；创建 workspace policy 后验证覆盖；归档后验证回退。
2. Preview 验证 archived 且超过保留期的 binary object 与旧 orphan 入选，不执行对象删除。
3. 验证 active Skill、Session artifact、未到期 archived Skill 和共享未到期引用均不入选。
4. 同 workspace 并发运行验证 advisory lock conflict；GC 执行期间发布 Skill version 必须等待同一锁。
5. 真实 localfs/S3 删除后验证 object body、metadata 和 `object_refs` 移除，tombstone 保留 digest、size、scanner provenance 和 operator。
6. 注入对象存储失败，验证 run/item failed 且 object ref 保留；下一次运行重试成功。
7. 预先删除对象 body，验证 `ErrNotFound` 被视为幂等删除，数据库收尾和 tombstone 成功。
8. 验证 `/run` 缺少 `confirm=DELETE` 返回 400，策略 disabled 返回 conflict，Preview 在 disabled 时仍可用。
9. 验证 `skills.asset_retention.policy_*`、`skills.asset_gc.preview/run/delete` audit 和 GC Prometheus 指标。
10. Workbench Storage 在桌面/移动 viewport 验证策略、候选、确认、运行和 tombstone，无溢出或 console error。

2026-07-13 结果：通过。

- HTTP 生命周期覆盖策略 create/publish/精确版本读取、有效策略、Preview、强确认 Run、历史与 operator audit。
- PostgreSQL 策略测试覆盖 workspace > organization > Server fallback、不可变 v1/v2、duplicate active conflict 和归档回退。
- PostgreSQL/localfs 覆盖 archived/orphan 删除、active 引用保护、并发锁、失败重试、对象缺失幂等和 scanner provenance tombstone。
- 真实 PostgreSQL/S3 验证 1 个 orphan 的 Preview、body/ref 删除和 tombstone；测试 workspace/object/policy/run 已清理。
- Workbench 在 `1280x720`、`390x844` 验证 Server fallback、临时 workspace policy 创建/归档、Preview 与禁用执行；无横向溢出和 console warning/error。
- 临时 `sarp_000005` 与本轮 audit 已精确删除；默认 workspace active retention policy、GC run、tombstone 均为 0。

### Offline Session Artifact ZIP

1. 上传标准 ZIP 为 Session Artifact，使用 `provider=artifact` + `artifact_id` Preview，验证不调用 GitHub 或任意 URL。
2. 验证根目录和单层包装目录；拒绝多层包装、缺失/多个 `SKILL.md`、traversal、反斜线、绝对路径、symlink、重复路径、未知扩展和各类 size/count/depth 超限。
3. 验证 Artifact 必须属于当前 Session/workspace、类型为 file、名称为 `.zip`，object ref size/checksum 必须与实际内容一致。
4. 验证 Artifact source 的 GitHub repository/ref check 为非强制通过，license、attestation、静态扫描、builtin binary scan、SBOM 和 Policy pin 仍完整执行。
5. 执行 Install，验证 `source_type=artifact`、`source_locator=session-artifact`、version Artifact ID/ZIP revision，以及标准 package objects/file refs。
6. 验证 text-only 包不会重复上传 asset；binary 仅在 builtin scan passed 后持久化，EICAR 在任何 registry/object 写入前阻止。
7. 验证 Artifact Skill 只能用同类新 Artifact 升级；GitHub/inline 替换和直接 inline upgrade 均拒绝。
8. 通过管理 API 完成 upload -> Preview -> Install，并验证 control audit 的 Artifact source。
9. 导出已存在标准 ZIP，上传并以临时 identifier 安装；下载新 package 后逐字节和 SHA-256 比对。
10. Workbench 在桌面/移动 viewport 验证“离线 ZIP”、file input、禁用/可用状态、Preview 安全区和无横向溢出。

2026-07-13 结果：通过。

- `TestParseArtifactPackage*` 覆盖标准包、安全边界与 builtin EICAR 阻断。
- `TestSkillsToolServicePreviewsAndInstallsOfflineArtifactPackage`、跨 Session和 size mismatch 测试通过。
- `TestSkillsMarketplaceHTTPOfflineArtifactUploadPreviewAndInstall` 覆盖真实 HTTP 生命周期。
- PostgreSQL/S3 实际安装为 `skl_000071` / `sklv_000099`，Artifact `art_000284`；下载回环 SHA-256 为 `836f7f252f8147ae5eb2f535e705843c882cfb4f8debcf86af1a3706134b42af`，与源 ZIP 一致。
- `1280x720` 与 `390x844` 的 document `scrollWidth == clientWidth`；移动 main `scrollWidth == clientWidth`，浏览器 console 无 warning/error。
- 浏览器截图接口两次超时，作为工具限制记录；DOM snapshot、可访问树与控件矩形检查均通过。

### Chat ZIP -> Preview -> Install -> Enable

1. 创建固定 Agent config version 且 `intervention_mode=request_approval` 的独立 Session。
2. 上传单个标准 ZIP，发送与 Workbench 相同的附件 payload；验证模型上下文包含 `artifact_id`，且明确禁止 `workspace_path`、主机路径、bucket/key 和 URL。
3. 验证模型先调用 `skills.preview`，参数只有当前 Session 的 `source.provider=artifact` 与 `artifact_id`。
4. Preview 为 `allowed + new_install` 后验证模型携带 policy pin 调用 `skills.install`，并停在独立 write approval；批准前 registry/version 均不写入。
5. 批准 Install 后验证只创建一个 version、Turn 续跑完成、Agent binding 不变化，并展示“请求启用”。
6. 点击“请求启用”，验证产生新的 `user.message` 和 `skills.enable` write approval，而不是前端直接调用 enable 管理 API。
7. 批准 Enable 后验证创建新 Agent config version，返回 `requires_session_upgrade=true`；当前 Session 仍固定旧版本。
8. 在 `1280x720` 与 `390x844` 检查附件卡、Preview 摘要、审批历史、安装结果、启用入口、文本换行、横向溢出和 console。
9. 归档临时 Skill/Session、恢复空 binding 配置，并删除 Session Artifact 与对应 object refs。

2026-07-13 结果：通过。

- 自动化：`TestDefaultContextBuilderAddsOfflineSkillZIPCoordinates`、`TestDemoRuntimeOfflineSkillZIPPreviewInstallAndApproval`、重复安装/显式升级/跨 Session/size mismatch/EICAR 零写入测试通过。
- 真实 Session `sesn_000220`：`art_000287` -> Preview allowed/new_install -> Install pending -> approved -> `tma-chat-skill` v1 -> Enable pending -> approved。
- Workbench 桌面与移动 DOM 无横向溢出，原始 CDP 移动画面无控件裁切，console 无 warning/error。
- 默认 Agent 已恢复到配置 v11、`skills.enabled=[]`；临时 Skill 已归档，Session 已终止，四个 Session Artifact/object refs 已删除。

### Enable -> Exact Session Config -> Runtime Resolve

1. 创建独立 Agent v1、idle Session 和可识别 marker 的 Skill v1。
2. 在 `request_approval` 下调用 `skills.enable`，批准后验证 Agent 生成 v2、Session 仍固定 v1、`requires_session_upgrade=true`。
3. 验证 Enable 成功卡展示目标 Skill/version、当前 Session v1、目标 config v2 和“应用到当前会话”。
4. 点击后验证请求使用 `to_version=2` 而非 `to_current`，并写入 `session.config_updated` old v1 -> new v2。
5. 验证按钮消失、卡片显示已生效、Runtime config 和工具目录立即刷新；重复读取时不创建新事件。
6. 发布并发 Agent v3 后，验证旧 Enable 卡仍只指向 v2；Session 高于目标时不得降级。
7. 发送正常用户消息，验证新 Turn 的 `runtime.skills_resolved` 包含精确 Skill/version 和 rendered 内容。
8. 在 `1280x720`、`390x844` 检查待应用/已生效状态、文本换行、按钮禁用、横向溢出和 console。

2026-07-14 结果：通过。

- HTTP 自动化覆盖 `to_current`、精确 `to_version`、双重/缺失目标、不存在版本和 idle 约束；CLI 覆盖两个互斥参数。
- 真实 `sesn_000224` 从配置 v1 精确升级到 v2，`latest_agent_config_version=2`；`session.config_updated` seq 76。
- 下一 Turn 的 `runtime.skills_resolved` seq 80 解析 `session-apply-e2e-20260714` v1，随后回复 `SESSION_CONFIG_APPLY_E2E_OK` 并回到 idle。
- 桌面和移动 DOM 均无横向溢出，原始移动截图无裁切，console 无 warning/error。
- 验收 Agent 当前 v3 为 `skills.enabled=[]`；临时 Session/Skill 已归档，两个工具结果 Artifact/object refs 已删除。

### Enable -> Disable -> Exact Session Config

1. 创建包含两个 Skill bindings 的独立 Agent 和固定旧配置版本的 idle Session。
2. 从已生效 Enable 卡点击“请求停用”，验证产生新的 `user.message` 和 `skills.disable` write approval，批准前 Agent config 不变化。
3. 批准后验证只移除目标 binding，另一个 Skill 及 LLM、Tools、MCP、System 保持不变，并返回被移除 binding 和精确 `new_config_version`。
4. 重复调用 Disable，验证 `removed=false` 且不增加 Agent config version。
5. 在 Disable 读取配置后模拟并发 Agent config 发布，验证 expected version conflict，且并发配置不被覆盖；基于最新配置重试后成功。
6. 验证 Disable 成功卡展示“应用到当前会话”，请求使用 `to_version`；应用前旧 Session 仍解析目标 Skill，应用后 Runtime config 不再包含目标 Skill。
7. 验证应用后的 Disable 卡展示“重新启用”，新 Enable 仍进入独立 write approval；旧生命周期卡标记 superseded 且不能应用。
8. 当前 Agent 仍绑定 Skill 时调用 Archive，验证 409 和先停用指引；发布移除 binding 的配置后归档成功，历史配置仍可读取。
9. 在 `1280x720` 和 `390x844` 检查停用审批、pending/applied/superseded 卡片、按钮换行、横向溢出和 console。

2026-07-14 结果：通过。

- 聚焦自动化覆盖 `skills.disable` manifest/write approval、只移除目标 binding、幂等、乐观并发冲突、管理 API/operator audit、Archive 保护和历史回放；PostgreSQL Skill registry 集成测试通过。
- `go test ./internal/tools ./internal/runner ./internal/agentruntime`、Skills 聚焦 HTTP/service 测试、`npm --prefix web-app run build` 和 `git diff --check` 通过；Server 重建重启后 `/health` 返回 `status=ok`。
- 真实 Agent `agt_000146` 的 Session `sesn_000229` 先精确应用 Enable config v2，再通过独立 `skills.disable` 审批发布 v3。Disable 调用 ID 为 `call_tau8xy8wsc7rz0qzxwkacij5`，intervention required/approved 为 seq 197/198，tool result 为 seq 201。
- Disable 结果为 `removed=true`、previous v2、new v3、Session v2、`requires_session_upgrade=true`；Agent v3 的 `skills.enabled=[]`。Workbench 精确应用 v3 后写入 `session.config_updated` seq 377，并只展示“重新启用”。
- v3 后续 Turn 的 `runtime.skills_resolved` seq 381 为 `skills=null`；旧生命周期卡没有“请求停用”或“应用到当前会话”，未发生陈旧配置应用。
- Skill Archive 在绑定存在时返回 409，解除 binding 后返回 200。验收 Skill/Session 已归档，三个临时工具结果 Artifact/object ref 已删除；不可变 package objects 保留。
- `1280x720` 与 `390x844` 均为 `horizontalOverflow=false`，可见控件没有 viewport 越界，截图无文本或按钮重叠。浏览器控制接口只支持 download/filechooser 事件，不支持 console/pageerror 监听，本轮 console 项标记为工具限制而非通过。
- 当前并行开发中的未跟踪 Agent portability/MCP fixture 使完整 `internal/httpapi` 包的 `TestAgentExportImportRoundTrip` 失败；该用例与 Skills 停用链路无关，本轮未修改相关文件。

### Workbench Agent/Session Binding Lifecycle

1. 创建一个包含 typed `inputs_schema` 的离线 Skill，并创建固定旧 Agent config 的 idle Session；确认整个流程不访问 GitHub 或任意公网 URL。
2. 在已安装详情选择精确 version，设置 mode、priority 和 typed inputs；验证 Enable 返回 `changed=true`、Agent 新增一个配置版本、Session binding 保持旧值。
3. 重复提交完全相同 binding，再使用只调整 JSON 对象键顺序的 inputs 提交；两次均验证 `changed=false`、HTTP `200` 且 Agent config version 不增加。
4. 修改 inputs、mode 或 priority，验证只新增一个配置版本；切换历史 Skill version 后验证 Agent binding 固定到所选精确 version。
5. 验证列表与详情同时显示 Agent 最新和 Session 当前 binding；不同配置显示“待应用”，Agent 已停用但 Session 未升级时显示“任务仍启用”。
6. 点击配置版本条的应用按钮，验证 Session 非 idle 时禁用；idle 时请求精确 `to_version=Agent current_config_version`，随后刷新 Session/runtime config 并变为已同步。
7. 直接停用目标 Skill，验证确认流程、`removed=true`、其他 bindings 与 Agent 其他配置不变；重复停用验证 `removed=false` 且不增加版本。
8. 停用后应用精确 Agent config，验证 Session runtime 不再包含目标 binding；Agent 仍绑定时 Archive 禁用，停用后可归档。
9. 模拟其他客户端已完成相同 Enable/Disable 后再从陈旧 Workbench 操作，验证幂等成功也会刷新 Agent metadata。
10. 在 `1280x720` 与 `390x844` 检查版本条、双 binding、typed form、确认按钮、状态标签、文本换行、横向溢出和 console。
11. 验收结束后恢复临时 Agent 为 `skills.enabled=[]`，归档临时 Skill/Session；保留不可变 package objects 供历史回放和 retention 管理。

2026-07-14 自动化结果：通过。

- Service/HTTP 覆盖首次变更、相同 binding、JSON 键序变化、Disable 和精确 Session config；Workbench helper 覆盖 pending/session-still-enabled/target-version 状态。
- `go test ./internal/httpapi ./internal/tools -run 'Skill|Skills' -count=1` 通过。
- `npm --prefix web-app test` 通过，共 70 个 tests。

2026-07-14 真实离线 E2E：通过。

- 资源为 Agent `agt_000152`、Environment `env_000171`、Session `sesn_000236`、Skill `skl_000084`，identifier 为 `lifecycle-e2e-20260714130826`；两个标准 `SKILL.md` package version 分别为 `sklv_000116` 和 `sklv_000117`。
- 配置序列为 Agent #1 -> Enable v1 的 #2 -> Enable v2 的 #3 -> Disable 的 #4。相同 binding 与 inputs 键序变化均保持 #2；重复 Disable 返回 `removed=false` 并保持 #4。
- Session 先从 #1 精确应用 #3；停用中间态保持 Session #3 和 v2 runtime binding，并显示“任务仍启用”；应用 #4 后 Session runtime `skills.enabled=[]` 且状态为已同步。
- `1280x720` 与 `390x844` 的 `documentOverflow=false`、`visibleOverflow=[]`，停用确认与停用中间态截图无控件、状态标签或文本重叠；TMA 页面 console error/warning 为空。
- 清理后 Skill 状态为 `archived`，Session 状态为 `terminated`，Agent config #4 保持 `skills.enabled=[]`。对象引用 `obj_000544/545/546/547` 仍存在，归档后的 v1/v2 package 下载均为 `200`。
- `go test ./...`、`go test ./internal/httpapi -count=1`、70 个 Workbench tests、production build 和 `git diff --check` 均通过；`/health` 为 `status=ok`，该 Session 在 Server 日志中无 ERROR。

### Internal Marketplace Entry Lifecycle

1. 为 active Skill 的精确不可变 version 创建市场草稿，验证摘要、分类和标签规范化，不复制 package/object refs。
2. 验证 `draft -> pending_review -> published -> withdrawn` 每一步成功，同状态重复请求幂等；直接草稿发布、回退或其他状态返回 `409`。
3. 草稿允许编辑；提交后 PATCH 返回 `409`，原展示元数据和精确 version 保持不变。
4. Skill 已归档或精确 version 不存在时禁止创建、提交或发布，且不得留下部分市场记录。
5. 同一 workspace 的同一 Skill 同时只能存在一个 `published` version；旧版下架后已审核新版可以发布。
6. 验证跨 workspace ID 读取、更新和状态动作返回 not found/forbidden，不泄漏条目元数据。
7. 验证 create/update/submit/publish/withdraw 的成功与失败 operator audit，包含 skill、version 和前后状态。
8. 在 Workbench 验证四态统计筛选、精确版本选择、状态流水线、审核意见、下架确认和终态只读状态。
9. 在 `1280x720` 与 `390x844` 检查五个 Skills tabs、列表编辑布局、生命周期状态和按钮无横向溢出或重叠。

2026-07-14 自动化结果：通过。

- HTTP 四态生命周期、非法跳转、草稿冻结、单发布版本和审计测试通过。
- 真实 PostgreSQL `TestPostgresSkillMarketplaceEntryLifecycle` 通过；migration `000056_skill_marketplace_entries.sql` 已应用。
- Workbench 76 个 Node tests 和 production build 通过，静态 bundle 包含“市场管理”和“提交审核”。
- 真实条目 `sment_000003` 引用 Skill `skl_000086` / version `sklv_000120`，通过 Workbench 完成草稿、待审核、已发布、已下架四态；审核意见、下架原因和三个标签回放一致。
- `1280x720` 和 `390x844` 均无 document 横向溢出；移动端 `visibleOverflow=[]`、`emptyButtons=0`，下架确认无按钮或文本重叠。
- 清理后默认市场条目数为 0，Skill 已归档，五条 operator audit 保留，`obj_000553/554` 对应 package 在归档后仍返回 `200`。

### Internal Marketplace Consumption

1. 在 publisher workspace 创建标准 `SKILL.md` package version，按 `draft -> pending_review -> published` 发布市场条目。
2. 在同 Organization 的 consumer Session 调用 `skills.discover` 空对象和 query/category/tags 筛选，验证默认 `provider=catalog`、`search_mode=organization_catalog` 和精确 `catalog_entry_id`，并确认 GitHub Client 未调用。
3. 用另一个 Organization 的 Session 执行相同 Discover/Get，验证返回空/not found，不泄漏标题、标签、Skill ID 或 checksum。
4. 用 catalog source Preview，验证只读、无公网请求，返回 ZIP revision、Policy pin、静态/builtin 二进制扫描、安全摘要和 package asset index。
5. 用 Preview pin 执行 Install，验证 consumer Skill provenance 为 `catalog / publisher skill_id / SKILL.md`，version source ref 为 entry ID、revision 为 ZIP SHA-256。
6. 发布方查看自己的条目时 Preview 为 `unchanged`；同一发布方 Skill 的后续 published entry 可在 `upgrade_existing=true` 下升级。
7. 发布条目存在时归档源 Skill 返回 conflict；下架后新 Discover/Preview/Install 失败，但 consumer 已安装 Skill 和不可变 package 保留。
8. 用非 superuser、无 `BYPASSRLS` 的 runtime role 重复同组织/跨组织读取，并验证 consumer 不能更新或删除发布方 Skill、version、package file 或市场条目。
9. 验证 operator 不能发布/下架，admin 可以；viewer 可浏览，member 可 Preview/Install，写安装仍进入独立 approval/audit。
10. 在 Workbench `1280x720` 与 `390x844` 检查四来源 segmented control、内部候选、Preview、安全摘要、安装按钮、横向溢出和文本重叠。

2026-07-14 自动化结果：通过。

- 隔离 PostgreSQL migration `000001` 至 `000063` 和 production runtime-role RLS 测试通过；覆盖同组织 browse、跨组织隔离、归档保护和下架隐藏。
- 真实 PostgreSQL + LocalFS `TestPostgresInternalMarketplaceHTTPPreviewAndInstallLocalFS` 通过，覆盖 TMA Discover、HTTP Browse/Preview/Install 和 consumer provenance。
- Catalog HTTP/Tool 聚焦测试、`go test ./...`、76 个 Workbench tests、production build 与 `git diff --check` 通过。

2026-07-14 真实 Workbench E2E：通过。

- Fixture：publisher `wksp_market_pub_20260714162240` / `skl_000091` / `sklv_000125`，entry `sment_000006`；consumer `wksp_market_consumer_20260714162240` / `agt_000156` / `env_000176` / `sesn_000241`。
- 内部市场默认发现 1 个候选；Preview 为 `new_install` 且 Policy allowed，Install 创建 `skl_000092` / `sklv_000126`，再次 Preview 为 `unchanged`。来源、entry ID、ZIP SHA-256 和 package asset index 均与发布方冻结版本一致。
- `1280x720` 的四来源为单行；`390x844` 为 2x2。两种 viewport 均为 `horizontalOverflow=false`，移动端 `visibleTextOverflowCount=0`，Preview 打开后无长文本越界，页面 console logs 为 `[]`。
- 下架 `sment_000006` 后 Discover 为 0、Preview 返回 `404`，消费者已安装 Skill 仍为 active；Workbench 刷新后候选为 0。随后两侧 Skill 已归档、Session 已终止，entry、审计和 `obj_000589` 至 `obj_000594` 保留。

### Internal Marketplace Version Upgrade

1. consumer 未安装时验证 Browse 候选为 `new_install`；安装 v1 后同一 entry 变为 `unchanged` 并返回本地 Skill/version/source ref。
2. 发布方创建 v2 草稿并提交，先下架 v1 再发布 v2；consumer Browse 应返回 `upgrade`、本地 v1 和新 entry ID。
3. Preview v2，验证本地 v1 -> 目标 v2、主文件变化及 added/changed/removed asset 路径，且安全扫描与 Policy pin 完整。
4. 不传 `upgrade_existing` 直接 Install，验证返回 `409` 且 consumer version 数不变。
5. Workbench 第一次点击“升级版本”只进入二次确认，确认前 API 仍只有 v1；取消不得写入。
6. 点击“确认升级”后验证创建 consumer v2、provenance 固定新 entry/ZIP revision，候选立即变为 `unchanged / 本地 v2`。
7. 已安装详情验证 v2/v1 同时存在、精确 package 可下载、两版均可重新启用；升级不自动修改 Agent binding 或 Session。
8. 在 `1280x720` 与 `390x844` 检查候选状态、diff、确认条、按钮宽度、文本换行和横向溢出。

2026-07-14 自动化与真实 Workbench E2E：通过。

- HTTP 内存测试覆盖 v1/v2 完整生命周期、明确升级参数、diff、provenance、旧版本保留和下架隐藏；前端 helper 测试覆盖候选状态和本地目标版本。
- `go test ./...`、84 项 Workbench tests、production build 和 `git diff --check` 通过。
- 真实 Fixture：publisher `skl_000093` / entries `sment_000007/000008`；consumer `agt_000159` / `env_000180` / `sesn_000245` / `skl_000094`。
- 确认前 consumer 只有 `sklv_000128` v1；确认后新增 `sklv_000130` v2，v1 仍可下载和“启用 v1”。桌面/移动均为 `horizontalOverflow=false`，移动 `visibleOverflow=[]`，console logs 为 `[]`。
- v2 下架后 Browse 为 0；两侧 Skill 已归档、Session 已终止，`obj_000602` 至 `obj_000615` 与审计记录保留。

## 2026-07-14 执行结果

- `go test ./internal/httpapi ./cmd/tma -run 'Test(UpgradeSessionAgentConfig|CommandSessionConfigUpgrade)' -count=1` 通过。
- `go test ./...` 全仓测试通过。
- `npm --prefix web-app run build` production build 通过。
- 真实 PostgreSQL、S3、LLM、审批、Workbench 精确 Session upgrade 和 Skill marker 验收通过。

## 2026-07-13 执行结果

- `go test ./internal/skillmarketplace ./internal/tools ./internal/httpapi -run 'Security|Attestation|Policy|Skills' -count=1` 通过。
- `go test ./...` 全仓测试通过。
- `TestPostgresSkillRegistryVersionsAndUsage` 和 `TestPostgresMarketplacePolicyVersionsAndPrecedence` PostgreSQL 集成测试通过。
- `TestPostgresSkillAssetRetentionPolicyVersionsAndPrecedence`、`TestPostgresSkillAssetRetentionGCDeletesEligibleObjects` 和真实 `TestPostgresSkillAssetRetentionGCS3E2E` 通过。
- Workbench production build 与 Storage 桌面/移动浏览器验收通过。
- Offline Artifact parser/service/HTTP、真实 PostgreSQL/S3 ZIP roundtrip 和 Workbench 响应式验收通过。
- `git diff --check` 通过。
- `/health` 返回 `status=ok`；默认 workspace Marketplace Policy 列表为空，无验收策略残留。

## 回归清理

- 验收 skill 使用唯一 identifier，完成后 archive。
- 验收 Session 完成后 archive，保留 event、intervention 和 usage 审计。
- 如果验收创建了 Agent binding，应创建后续空 binding config version 恢复默认 Agent。
- 检查 `/health`、Server 日志中验收 Session 无 ERROR，并确认默认 Agent 当前配置未残留测试 skill。
