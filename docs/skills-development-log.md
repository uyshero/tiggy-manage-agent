# Skills 模块开发记录

## 2026-07-13：Registry 与 Runtime 基线

完成内容：

- 定义 canonical Agent skill binding，固定 identifier、version、mode、priority 和 inputs。
- 实现 skill registry、不可变 version、上下文 renderer、token budget 降级与 turn usage。
- 增加 Postgres migration `000032_skills.sql`。
- 增加 Registry HTTP API、resolve preview、usage API 和 runtime 事件。

关键决策：

- Session 固定 Agent config version，skill enable 只创建新配置，不热改正在运行的 Session。
- Runtime 必须按精确 skill version 解析，避免 latest 漂移导致回放不一致。
- legacy skills 配置只保留读取兼容，不允许作为新写入格式。

## 2026-07-13：TMA 对话工具

完成内容：

- `skills.search`：查询 workspace 已安装 registry。
- `skills.inspect`：读取精确版本或最新版本。
- `skills.install`：安装 inline package 或发布升级版本。
- `skills.enable`：创建 Agent config version 并返回 Session upgrade 提示。
- install/enable 使用 write risk，复用 Session intervention policy。
- workspace 由 ExecutionContext 和 Session 双重确认，模型参数不能跨 workspace。

真实验收：

- Session `sesn_000167` 完成 search -> install approval -> enable approval -> inspect。
- Session 升级后产生 `runtime.skills_resolved`，usage 状态为 `resolved`。
- 验收 skill 和 Session 已归档，默认 Agent 已恢复为空 binding。

## 2026-07-13：GitHub Marketplace

完成内容：

- 新增 `skills.discover`，有 token 时优先 GitHub code search，无 token 时降级 repository search。
- GitHub source 只接受 `owner/repo`、可选 ref 和仓库内 `SKILL.md` 相对路径。
- 支持公开仓库和 `TMA_SKILLS_GITHUB_TOKEN` 可访问的私有仓库。
- 增加 provenance：skill 保存 source type/repository/path；version 保存 ref/blob revision/source URL。
- 禁止任意 URL、路径穿越、inline 覆盖 GitHub skill，以及跨 repository/path 升级。
- 增加 migration `000035_skill_marketplace_sources.sql`。

真实验收：

- Session `sesn_000169` 调用 discover 后，从 `anthropics/skills` 的 `skills/pdf/SKILL.md` 安装 version 1。
- 保存 revision `d3e046a5ae107a6cb23cfb16c219837094ab35d3` 和 canonical GitHub URL。
- 验收 skill 未 enable，随后已归档；默认 Agent 配置未变化。

## 2026-07-13：Package Assets

完成内容：

- GitHub 安装递归抓取 `SKILL.md` 明确引用的同 package 文本文档、脚本和配置。
- 限制最多 32 个依赖文件、4 层目录、单文件 100000 bytes、依赖总量 256 KiB。
- 外部 URL、包外路径和不支持的二进制引用不抓取，并写入 package warnings。
- 脚本只作为 reference text 保存，标记 `executable=true`，不会自动运行。
- 新增标准 asset bundle schema 和 `skills.read_asset` 只读工具。
- Renderer 只注入 asset index；search/inspect/install 只返回索引，正文按需读取。
- GitHub GET 对网络错误、HTTP 429 和 5xx 最多重试 3 次；支持并限制 `Retry-After` 等待，404 不重试。
- 对 `REFERENCE.md`、`FORMS.md` 等大小写不一致引用，先读取目录清单解析真实路径，规避 GitHub Contents API 对错误大小写持续返回 502 的异常。

真实验收：

- Session `sesn_000171` 发现反引号命令被误识别为完整路径，修复为按命令 token 提取依赖。
- Session `sesn_000172`、`sesn_000173` 分别验证瞬时和持续 GitHub 502，推动增加有界重试和大小写路径预解析；失败均发生在 registry 写入前。
- Session `sesn_000174` 完成 GitHub install approval -> package closure -> `skills.read_asset(reference.md)`。
- version 1 保存 9 个 assets、总计 49094 bytes，包括 `LICENSE.txt`、`forms.md`、`reference.md` 和 6 个 Python 脚本；revision 为 `d3e046a5ae107a6cb23cfb16c219837094ab35d3`。
- install result 仅返回 asset index，`skills.read_asset` 成功返回正文；未调用 enable，默认 Agent config 仍为空 binding。
- 验收 skill 与 Session 已归档。

## 2026-07-13：Package Preview

完成内容：

- 新增只读 `skills.preview`，接受与 GitHub install 相同的受限 source 坐标，不写 registry。
- 返回建议 identifier、title、description、frontmatter license、canonical source、revision、source URL、主文件字节数、warnings 和脱敏 asset index。
- 自动查询当前 workspace 同 identifier skill；返回 `new_install`、`upgrade`、`unchanged` 或 `blocked`。
- upgrade diff 包含主内容是否变化，以及新增、删除、变更的 asset 路径；不返回主文件或 asset 正文。
- 归档 skill、GitHub provenance 不一致和无已发布版本会返回明确 `block_reason`。
- preview 与 install 共用标准 asset bundle 转换，避免预览结果和实际安装清单漂移。

真实验收：

- Session `sesn_000176` 验证 preview 在 `request_approval` 下直接执行、无 intervention；匿名 GitHub core API 配额耗尽后返回 403，未写 registry。
- Server 使用本机 `gh` 登录 token 重启后，Session `sesn_000177` 成功调用 `skills.preview`。
- 返回 `install_state=new_install`、license `Proprietary. LICENSE.txt has complete terms`、revision `d3e046a5ae107a6cb23cfb16c219837094ab35d3`、9 个 assets 和 49094 bytes。
- preview asset index 中 asset body 字段数为 0，install/enable 调用数为 0，目标 identifier registry 记录数保持为 0。
- 验收 Session 已归档。

## 2026-07-13：Marketplace Policy

完成内容：

- 新增纯 `skillmarketplace.Policy` evaluator，统一输出 `allowed`、结构化 checks 和 violations。
- 支持 GitHub owner/repository allowlist、完整 40 字符 commit SHA ref pin、license allow/deny 和 license 必填策略。
- repository/ref policy 在 GitHub fetch 前执行；license policy 在 frontmatter 读取后执行。
- `skills.preview` 将 policy decision 合入结果并返回 `blocked`；`skills.install` 强制同一策略，违规返回 forbidden。
- policy 拒绝发生在 registry 写入前；source policy 拒绝还会避免向不允许的 GitHub repository 发请求。
- 启动变量默认均不启用，保持已有公开仓库、branch ref 和无 license package 的兼容行为。

真实验收：

- Server 临时配置 `anthropics` owner allowlist、license 必填并 deny `Proprietary`，Session `sesn_000179` 预览 `anthropics/skills` PDF package。
- preview checks 显示 repository allowlist 通过、ref pin 未启用、license deny 失败；整体 `policy.allowed=false`、`install_state=blocked`，且 preview 不产生 intervention。
- 同一 Session 故意调用 install 并批准写操作后，服务端返回 `forbidden: skill marketplace policy blocked install`。
- identifier `tma-github-policy-e2e-20260713` 在验收前后 registry 记录数均为 0；Session 已归档。
- 验收后 Server 已恢复默认 permissive policy，GitHub token 仅保留在运行进程环境中，未写入 `.env`。

## 2026-07-13：Versioned Workspace/Organization Policy

完成内容：

- 新增 migration `000036_skill_marketplace_policies.sql`，包含 scope metadata、active partial unique index 和 immutable policy versions。
- 有效策略优先级固定为 workspace > organization > Server env fallback；归档 workspace policy 后自动回退 organization。
- 新增 control-plane create/list/get/publish-version/archive API，所有写操作记录 operator audit。
- preview 返回 `policy_source`、`policy_id`、`policy_version` 和 checksum `policy_revision`。
- 持久化策略下 install 强制携带 preview pin；缺失 pin或审批期间 revision 变化返回 conflict，要求重新 preview。
- preview/install policy evaluation 写入 session operator audit，包含 source、package revision、checks、允许/拒绝结果和 turn ID。

真实验收：

- 创建 workspace policy `smpol_000004` version 1，Session `sesn_000183` preview 返回 version 1 revision `b52ba64fcfcf48aabeceb4afc0feda9ecac556aa90e12e627300f3407a029b74`。
- 发布 version 2 后，使用 v1 pin 的 install 在审批通过后返回 `marketplace policy changed` conflict，registry 记录数保持为 0。
- 重新 preview 获得 version 2 revision `25b80eea06ad5f08862e1418a76191d7762090b4b51d7043cde9791d806e703b`，同 pin install 成功。
- operator audit 保留 v1/v2 preview、stale pin 拒绝和 v2 install 成功记录；验收 skill、Session 和 policy 均已归档。

## 2026-07-13：Package Attestation and Static Scan

完成内容：

- 新增规范化 package SHA-256，覆盖主文件与排序后的文本 assets，使用路径/内容长度前缀并排除 attestation 自身。
- 支持 `tma.skill.attestation.v1` Ed25519 attestation、trusted key ID、digest 和签名验证。
- 新增静态扫描规则与 `medium/high/critical` 严重度，finding 最多 50 条且不回显恶意原文。
- versioned policy 新增 `require_attestation`、`trusted_attestation_keys` 和 `static_scan_block_severity`；字段进入 policy checksum/revision。
- Preview 返回 security report；install 重新计算 digest、签名和 findings，不信任客户端 preview 数据。
- 无效 attestation 始终阻止；缺失/untrusted attestation 由 require policy 决定；静态扫描默认只报告。
- Security report 与 policy checks 一并写入 operator audit。

真实验收：

- Session `sesn_000186` 预览 Anthropic PDF package：digest `30aa13e4ecf89f337eadcdd92e34b96594b96df53fe61d6c0183b867eda21f89`，扫描 10 个文件、0 findings，attestation missing；默认 policy 不阻止。
- 临时 workspace policy `smpol_000008` 强制 attestation 并设置 high scan threshold；Session `sesn_000187` 对同一 package 返回 `install_state=blocked`，原因是 attestation missing。
- 两个 Session 均无 install/enable 和 registry 写入；Session 与临时 policy 已归档。
- 最终聚焦测试、`go test ./...` 及 Skills Registry/Marketplace Policy PostgreSQL 集成测试全部通过；默认 workspace 查询确认无 active persisted policy。

## 2026-07-13：Skills Management UI

完成内容：

- Workbench 设置中心新增“已安装 / Marketplace / Policy”三视图 Skills 管理页。
- 已安装视图支持搜索、版本与 provenance 查看、Agent 启用和二次确认归档。
- Marketplace 视图支持关键词/精确仓库发现、只读 Preview、Policy checks、Attestation、静态 findings、package diff、安装与升级。
- Policy 视图支持 workspace/organization policy 创建、不可变新版本发布、归档回退，以及 allowlist、license、commit pin、trusted keys 和扫描阈值编辑。
- 新增 control auth 管理 API，复用 `skillsToolService` 的 discover/preview/install/enable；install 不信任 UI Preview，重新抓取并执行安全检查。
- Marketplace install 和 Agent enable 写入 control principal operator audit；Session 继续作为 workspace 隔离边界。
- Vite 构建产物更新到 `internal/httpapi/app`，Go 静态资源测试检查 Package Security 和 Marketplace Policy 标识。

真实验收：

- 浏览器在 `1280x720` 与 `390x844` 完成已安装、Marketplace、Policy 三视图验收，无页面或容器横向溢出。
- 精确来源支持 `owner/repo`、ref 与嵌套 `SKILL.md` 路径；真实预览 Anthropic PDF package 成功展示 Policy、Attestation、Digest、10 文件扫描和 9 个新增 assets。
- Preview 全程未调用 install/enable，workspace registry 记录数保持为 0；浏览器控制台无错误。

## 2026-07-13：Controlled Binary Assets、Object Store 与 SBOM

完成内容：

- Package closure 新增显式引用的 PNG/JPEG/GIF/WebP、PDF、DOCX、XLSX、PPTX；文本单文件上限保持 100000 bytes，二进制单文件上限 512 KiB，package assets 总量提升到 4 MiB。
- `PackageFile` 支持短暂的 Base64 body、检测 MIME、SHA-256 与 binary 标记；package digest 按解码后的原始字节计算。
- 新增 `tma.skill.binary-scan.v1`，校验编码、size/digest、扩展/MIME，阻止 PE/ELF/Mach-O、EICAR、PDF active/embedded content 和 Office macro marker。
- binary scan 是强制 Policy check，任何 blocked 文件都禁止 Preview 安装状态和实际 Install，不受静态扫描阈值影响。
- 新增 `tma.skill.sbom.v1`，覆盖 `SKILL.md` 与 package closure 中全部 text/binary components；持久化后 binary component 关联 object ref。
- Preview 只返回脱敏 asset index、scan result 和 SBOM，不写对象存储。Install 重新抓取/扫描，通过后上传默认 object store bucket 并创建 workspace object ref。
- version asset bundle 只保存 binary `object_ref_id`、MIME、size、SHA-256、scan status 和 SBOM，不保存 Base64 body；未提交安装会逆序清理已上传对象和 object refs。
- `skills.read_asset` 明确拒绝 binary；Workbench 仅展示 metadata，并通过 `/v1/object-refs/{id}/download` 提供 Session 作用域受控下载，不自动内联预览。
- 真实 `theme-factory` 验收推动 inline-code reference 同样支持 binary allowlist，并把 PDF active-content 扫描收紧为 stream 外的 PDF Name token；ASCII-only case folding 保留任意压缩字节长度，避免 Unicode lower 偏移导致 panic。

自动化验收：

- GitHub closure 测试覆盖仅显式引用下载、allowlist 和二进制限额。
- Security 测试覆盖安全 PNG、EICAR、MIME 伪装、digest 原始字节敏感性和 SBOM。
- HTTP service 测试覆盖 Preview 零对象写入、Install 单次上传、持久化 object ref/Base64 清除、binary read forbidden、危险文件零写入阻断和发布前失败回滚。
- Vite production build 通过，Workbench 生产静态包已包含二进制数量、SBOM 和逐资产扫描状态。

真实验收：

- Session `sesn_000191` Preview `anthropics/skills@main:skills/theme-factory/SKILL.md`，closure 包含 `LICENSE.txt` 与反引号引用的 `theme-showcase.pdf`。
- PDF 为 124310 bytes、`application/pdf`、SHA-256 `3e126eca9fe99088051f7cb984c97cedb31c7d9e09ce0ba5d61bd01e70a0d253`；binary scan passed、0 findings，SBOM 共 3 components。
- Preview 前后目标 identifier 和 Skill object refs 均为 0；Install 创建临时 `skl_000033` v1 与 `obj_000258`，数据库 bundle 无 `content_base64`，binary 与 SBOM component 均引用同一 object ref。
- TMA 代理下载 SHA-256 与 GitHub/registry 一致。Workbench 在 1280x720 和 390x844 展示 binary count、SBOM、MIME、digest、passed 与受控下载，无横向溢出、自动内联或控制台错误。
- 验收完成后归档测试 Skill，删除对象/ref，并按唯一 ID 清理测试 registry 行；目标 Skill/object ref 均为 0，下载返回 404。

## 2026-07-13：External ClamAV/Enterprise Binary Scanner

完成内容：

- 新增 `skillmarketplace.BinaryScanner` 契约和 `clamav_http` Provider；默认 `builtin` 保持零外部依赖。
- HTTP contract 支持同步 200 verdict 和异步 202 pending + `scan_id` 轮询；网络错误、429、5xx 有限重试，单文件共享总 timeout。
- 外部 scanner 只在 Install 重抓 package、内置 Policy 全部通过后调用；Preview 不调用外部服务，仍保持只读与确定性。
- 只有内置和外部 scanner 都 passed 才允许 object PUT。blocked 返回 forbidden；error、timeout、协议错误和重试耗尽 fail closed，且 object/ref/registry 零写入。
- `PackageSecurityReport.binary_files[].external_scan` 保存 provider、status、scanner、scan ID、signature、attempts 和 duration；version asset/object metadata 保存 `scan_provider` / `scan_version`。
- 新增 `skills.binary_scan` operator audit，以及 `tma_skill_binary_scans_total`、`tma_skill_binary_scan_duration_milliseconds_total` Prometheus 指标。
- Workbench 已安装版本展示外部 scanner provider/version；仍不自动内联或请求 binary body。
- 新增标准库开发 fixture `scripts/clamav_http_fixture.py`，覆盖 pending -> passed 和 EICAR blocked，不进入生产 server。

真实验收：

- 使用 `clamav_http` fixture 与真实 `anthropics/skills@main:skills/theme-factory/SKILL.md`，Preview 仅返回 builtin scan，目标 Skill/object ref 前后均为 0。
- Install 对 124310-byte PDF 执行 POST 202 + GET poll，最终 `external_scan.status=passed`、scanner `ClamAV fixture 1.0`、attempts 2。
- 临时 `skl_000036` v1 / `obj_000259` 的 bundle 无 Base64，asset 和 object metadata 均保存 `clamav_http / ClamAV fixture 1.0`；代理下载 SHA-256 与 registry 一致。
- operator audit `skills.binary_scan` outcome succeeded，Prometheus passed counter 为 1、累计 duration 25ms；fixture EICAR 请求返回 blocked 和 `Eicar-Signature`。
- Workbench 在 1280x720 与 390x844 展示 scanner provenance，无横向溢出或 console warning/error。
- 验收结束后测试 Skill/version/object/ref 已清零，fixture 已停止；Server 恢复默认 builtin 配置。

## 2026-07-13：Binary Asset Retention and GC

完成内容：

- 新增 `000038_skill_asset_retention.sql`，包含 organization/workspace 策略、不可变版本、GC run/item 和 durable tombstone。
- 有效策略优先级为 workspace > organization > Server fallback；Server 策略和自动 worker 默认关闭。
- 候选检测覆盖超过保留期的 archived Skill binary assets 与 orphan `skill_asset` object refs；Session artifact、active Skill 和尚未到期的共享 archived 引用会阻止删除。
- 每个 workspace 使用 PostgreSQL advisory lock；Skill 新版本发布与 GC 使用同一锁，执行项在对象删除前再次验证引用。
- 对象删除后在事务内删除 `object_refs` 并写 tombstone；tombstone 保存 Skill/version/path、对象位置、SHA-256、size、scanner provenance、原因和 operator。
- 对象存储失败保留 object ref 并把 item 标记为 failed，后续运行自动重试；对象已不存在按幂等成功完成数据库收尾。
- 新增 effective/policy/preview/run/history/tombstone control API、operator audit 与 Prometheus GC run/object/bytes/candidate 指标。
- Workbench Skills 新增 `Storage` 视图，可发布 workspace 策略、dry-run 预览、二次确认执行，并查看运行与 tombstone。

自动化验收：

- HTTP 生命周期覆盖策略创建/发布、有效版本、Preview、`confirm=DELETE` 强确认、运行列表和 operator audit。
- PostgreSQL/localfs 集成覆盖 archived/orphan 删除、active Skill 保护、advisory lock 冲突、扫描来源 tombstone、失败重试与对象缺失幂等收尾。
- localfs 删除语义与 S3 对齐：对象和 metadata 均不存在时返回 `objectstore.ErrNotFound`。

真实验收：

- PostgreSQL/S3 使用独立 workspace 和唯一 `skills-gc-e2e/.../orphan.pdf`，Preview 精确返回 1 个 orphan candidate。
- GC run 为 `succeeded`、deleted 1；S3 GET 返回 `objectstore.ErrNotFound`，数据库 object ref 返回 not found，tombstone 保存 `s3-e2e / 1.0` scanner provenance。
- Workbench Storage 在 `1280x720` 与 `390x844` 验证 Server fallback、策略创建/归档回退、Preview、禁用执行、运行/tombstone 空状态；页面和内部容器无横向溢出，console 无 warning/error。
- 临时 workspace policy `sarp_000005`、5 条测试 audit、S3 object/ref、独立 workspace/run/tombstone 已全部清理；默认 workspace active retention policy、GC run 和 tombstone 均为 0。

## 2026-07-13：External Scanner 暂缓开放

当前发布调整：

- 生产 `NewBinaryScanner` 工厂只允许不联网的 `builtin`；`clamav_http` 固定返回 `ErrExternalBinaryScannerDeferred`。
- ClamAV HTTP client、fixture、协议与 provenance 测试保留，作为未来完成私网 egress 防护后的开发基础。
- `.env.example` 撤下 ClamAV endpoint/token/retry 配置，现行配置与 API 文档明确 `external_scan` 仅为预留字段。
- 新增零请求测试：即使提供有效 HTTP endpoint，外部 provider 工厂也不构造 scanner，测试服务请求计数保持 0。
- GitHub Marketplace 不受此调整影响；限制仅针对安全扫描子系统发送二进制文件。

验证结果：

- `TestNewBinaryScannerDefersExternalProvider` 通过，全仓 `go test ./...` 通过，`git diff --check` 通过。
- Server 重启后日志为 `using built-in skills binary scanner`，`/health` 返回 `ok`。

## 当前边界

- 当前 package closure 基于 Markdown 链接、反引号相对路径和大写文档名，不扫描仓库全部文件。
- GitHub 重试只覆盖幂等 GET，最多 3 次；持续性上游故障仍会终止安装，且不会写入部分 registry 数据。
- GitHub repository search 只能发现仓库 metadata，候选为 `verified=false`；code search 需要 token。
- 安装新 skill 与发布首个 version 仍是两次 Store 调用，极少数数据库中断可能留下无 version 的 metadata。
- 默认 builtin 扫描基于确定性规则和文件 marker且不联网；外部 Scanner/ClamAV 已从当前生产工厂禁用，仅保留开发实现和历史测试。重新开放前需要私网 allowlist、redirect 拒绝、最终 IP 校验和 DNS rebinding 防护。
- `tma.skill.sbom.v1` 是内部最小格式，尚未输出 SPDX/CycloneDX，也未实现外部 Sigstore/SLSA provenance。
- GC 处理 `metadata.kind=skill_asset` 的孤儿对象，以及归档 Skill version 明确引用的 binary/package 文件；不处理通用 Session artifact 或其他 object kind。
- 历史 archived Skill version 的 `assets_json` 保持不可变并继续保存已删除的 object ref ID；调用方应通过 tombstone 查询删除事实，原下载端点返回 404。

## 2026-07-13：标准 SKILL.md 文件包存储

完成内容：

- 新增 `000040_skill_package_storage.sql`，Skill version 保存 package format/root/checksum、ZIP 与 `SKILL.md` object ref，以及逐文件引用索引。
- 新版本以 `skills/<workspace>/<identifier>/versions/<version>/` 为不可变根目录，直接保存 `SKILL.md` 和文本 assets；`.tma/package.zip` 包含完整标准目录，binary 文件复用已扫描 object ref。
- `internal/skillpackage` 使用固定 ZIP 时间、权限和排序生成确定性 archive；package checksum 对路径与原始字节做长度前缀摘要。
- PostgreSQL 发布事务先物化对象，再原子写入 version、object refs 和 package files；任一数据库错误会清理新对象，已存在 binary ref 不被误删。
- `GetSkillVersion` 优先读取 object storage 的 `SKILL.md`，读取失败回退 `content_text`；本阶段保留数据库正文和 assets 以兼容旧 runtime、回放与 Agent 配置校验。
- 新增 `POST /v1/skill-packages/backfill`，可按 workspace/limit 幂等迁移 legacy version；新增 version package ZIP 下载 API。
- package backfill 写入 `skills.package_storage.backfill` operator audit，记录 workspace、limit、scanned、migrated 和失败结果。
- Skill package files 接入 retention GC：active Skill 与未到期 archived version 受保护；到期归档包清理文件引用、object ref 并写 durable tombstone。
- Workbench 已安装版本显示 package format、对象根路径和文件包下载入口。

验证结果：

- `internal/skillpackage` 测试验证标准目录、独立 `SKILL.md`、文本 assets、可执行权限和 ZIP/checksum 确定性。
- HTTP 测试验证 package 下载文件名、content type、checksum header，以及 legacy version 返回 404。
- PostgreSQL/localfs E2E 验证 legacy 回填、三条文件引用、幂等重复回填，以及把 DB 正文改成 stale 后仍读取对象存储原始 `SKILL.md`。
- localhost S3 回填现有 4 个 legacy versions，全部迁移成功并建立 26 条文件引用；抽样 ZIP 下载为 HTTP 200，checksum header 与数据库一致。
- localhost S3 新发布临时 v1，直接生成 `SKILL.md`、`references/guide.md` 和 archive；实际 GC 删除 3/3 objects、429 bytes，均非 missing。
- Workbench 在 `1280x720` 与 `390x844` 显示 package format/root/download，页面和详情面板均无横向溢出，console 无 warning/error。
- 临时 Skill `skl_000062`、policy `sarp_000023`、run `sagcr_000034`、3 tombstones 和 6 条测试 audit 已清理；保留的 4 个历史 versions 均为 `tma.skill-package.v1`。
- `go test ./...`、package 聚焦 PostgreSQL E2E、HTTP/skillpackage tests、`npm --prefix web-app run build` 和 `git diff --check` 通过。

## 2026-07-13：Session Artifact 离线 ZIP 导入

完成内容：

- 新增 `artifact` Marketplace source；TMA 对话和管理 API 使用当前 Session 的 `artifact_id`，不接受主机路径、任意 URL、bucket/key 或跨 Session Artifact。
- 新增内存 ZIP parser：archive 最大 8 MiB，恰好一个根目录或单层包装目录 `SKILL.md`；拒绝 traversal、反斜线、绝对路径、NUL、symlink、重复路径、未知扩展和超限解压内容。
- Artifact package 复用 GitHub 的 license、attestation、静态扫描、builtin binary scanner、SBOM、Policy pin、write approval、binary object persistence 和标准版本发布。
- 服务端校验 Artifact/Session/workspace 归属、file 类型、`.zip` 扩展、object ref size 和 SHA-256；ZIP SHA-256 作为 source revision。
- 新增 `000042_skill_artifact_sources.sql`；provenance 固定为 `source_type=artifact`、`source_locator=session-artifact`、`source_path=SKILL.md`，version `source_ref` 为 Artifact ID。
- Artifact-sourced Skill 允许从当前 Session 的新 Artifact ID 发布升级，但拒绝 GitHub/inline 来源替换。
- Workbench Marketplace 增加“离线 ZIP”，复用 Session Artifact 上传后自动 Preview，再使用 Preview source 安装。

验证结果：

- parser、service 和 HTTP 自动化覆盖根/包装目录、危险 ZIP、EICAR、跨 Session、对象 size 篡改，以及 upload -> Preview -> Install 全链路。
- `go test ./internal/skillmarketplace ./internal/httpapi ./internal/managedagents` 和 Skills 工具测试通过；Vite production build 与 `git diff --check` 通过。
- 真实 PostgreSQL/S3 使用 `sesn_000197` 上传 `art_000284`，Preview 扫描 1 文件、0 findings、Policy allowed；安装 `skl_000071` v1 保存 Artifact provenance 和两个标准 package object refs。
- 原导出 ZIP与安装后下载 ZIP SHA-256 均为 `836f7f252f8147ae5eb2f535e705843c882cfb4f8debcf86af1a3706134b42af`，逐字节一致；临时 Skill 验收后已归档。
- Workbench 在 `1280x720` 和 `390x844` 验证三段模式控件、`.zip` file input 与安全预览按钮；document/main 无横向溢出，控制台无 warning/error。浏览器截图接口超时，DOM、可访问树和几何检查已完成。
- Server 重启后 `/health` 为 `ok`，日志确认使用 S3 package storage 和 builtin skills binary scanner。

## 后续建议

1. 增加 Office archive 深度检查、scanner 集群健康状态与病毒库版本告警。
2. 输出 SPDX/CycloneDX，并对接 Sigstore/SLSA provenance、组织级 key rotation/expiry。
3. 增加 Policy 模板、跨 workspace 批量审批、tombstone 导出和 key 到期提醒。

## 2026-07-13：聊天附件自动安装闭环

完成内容：

- Context Builder 为上传附件加入 Session 级 `artifact_id`，并明确 ZIP 只能通过 `source.provider=artifact` 进入 Skills 工具；`workspace_path`、主机路径、bucket/key 和 URL 不可作为安装来源。
- Skills 工具系统提示固定 Preview -> Install -> 用户请求后 Enable 的顺序；`blocked` / `unchanged` 不安装，升级必须设置 `upgrade_existing=true`，Install 与 Enable 使用独立 write approval。
- Workbench 在单个 ZIP 且无文本时自动生成离线安装请求，专门展示 Preview Policy/扫描摘要、Install 风险与安装结果。
- 安装成功结果默认展开并提供“请求启用”；该按钮发送新的对话请求调用 `skills.enable`，不会绕过 Session 审批 API。
- 安装成功后自动刷新已安装 Skills，移动端安装后续操作改为全宽布局。

自动化验证：

- `TestDefaultContextBuilderAddsOfflineSkillZIPCoordinates` 验证 Artifact ID 和禁止路径指引进入模型上下文。
- `TestDemoRuntimeOfflineSkillZIPPreviewInstallAndApproval` 验证 Preview 先执行、Install 停在审批、批准后只安装一次并续跑。
- Offline Artifact service 覆盖重复安装冲突、新 Artifact 显式升级、跨 Session、size mismatch 和 EICAR 零写入阻断。

真实验收：

- Session `sesn_000220` 上传 `art_000287`，模型先调用 `skills.preview`，返回 `new_install`、Policy allowed、扫描 1 个文件和 0 findings。
- 模型随后使用同一 Artifact 和服务端 `policy_revision` 调用 `skills.install`，在 `request_approval` 下等待批准；批准后创建 `tma-chat-skill` v1，Turn 正常完成且未自动 Enable。
- Workbench 展示 ZIP 附件、Preview 安全摘要、Install 审批历史、安装结果和“请求启用”。Enable 从新 Turn 进入第二次审批并发布 Agent config v10；后续空 binding 配置 v11 已恢复默认 Agent。
- `1280x720` 与 `390x844` 验证无 document 横向溢出；移动原始 CDP 截图、DOM 几何和 console 均通过。浏览器通用 full-page 截图在 2x DPR 下存在缩放显示问题，不属于页面布局。
- 验收结束后 Skill `skl_000075` 已归档、Session 已终止，四个 Session Artifact 及对象引用已删除；默认 Agent 当前 `skills.enabled=[]`。

## 2026-07-14：Enable 后应用到当前 Session

完成内容：

- Session config upgrade API 新增 `to_version`，可事务内把 idle Session 升级到同一 Agent 的精确配置版本；原 `to_current=true` 和空请求兼容保留。
- 精确升级拒绝双重/空目标、不存在版本和版本降级；事件增加 `latest_agent_config_version`，便于区分目标版本与 Agent 最新版本。
- CLI 新增 `session config upgrade --to-version VERSION`，并与 `--to-current` 互斥。
- Workbench 在 `skills.enable` 成功卡片展示“应用到当前会话”，提交 Enable 返回的 `new_config_version`，避免并发 Agent 配置发布造成错误升级。
- 升级成功后立即刷新 Session、事件和 Runtime config；卡片原地显示“Skill 已生效”，工具目录同步更新。
- 已有更高 Session 配置时不允许从旧 Enable 卡降级；如果当前 Runtime config 仍包含精确 Skill binding，则按实际 binding 显示已生效，否则提示到 Agent 设置确认。
- Enable 成功后旧 Install 卡不再重复显示“请求启用”，避免重复发布 Agent config version。

真实验收：

- 独立 Agent `agt_000140` 的 Session `sesn_000224` 固定 v1，`skills.enable` 经审批发布配置 v2，并返回 `requires_session_upgrade=true`。
- Workbench 显示 `session-apply-e2e-20260714` v1 的“应用到当前会话”；点击写入 `session.config_updated` seq 76，payload 为 old v1 -> new v2、latest v2。
- 后续 Turn 的 `runtime.skills_resolved` seq 80 精确解析 `skl_000076` / `sklv_000106` v1，`rendered_mode=full`、estimated tokens 76。
- 模型按 Skill 指令返回 `SESSION_CONFIG_APPLY_E2E_OK`，证明不是仅更新 UI 状态，而是新配置已进入 Runtime 上下文。
- Workbench 在 `1280x720` 与 `390x844` 无横向溢出，卡片文本和 composer 不重叠，console 无 warning/error。
- 验收后 Agent `agt_000140` 发布空 binding v3，Session 与 Skill 已归档，Inspect/Enable Artifact 和 object ref 已删除；标准 package objects 按历史回放与 retention 规则保留。

## 2026-07-14：Skill 停用与归档保护闭环

完成内容：

- 新增 `skills.disable` server builtin tool，参数只包含 `identifier`，风险为 `write`，因此在 `request_approval` 下与 Enable 一样产生独立审批。
- Disable 读取当前 Session 所属 Agent 的最新配置，只移除目标 binding，保留其他 Skills、LLM、Tools、MCP 和 System；成功发布不可变的新 Agent config version。
- 重复 Disable 返回 `removed=false` 且不创建配置版本；成功响应保留被移除 binding 的 version/mode/priority/inputs，供重新启用使用。
- Enable/Disable 使用 `expected_current_version` 乐观并发保护；读取后发生其他 Agent 配置发布时返回 conflict，旧快照不能覆盖并发改动。
- Workbench 在已生效 Enable 卡展示“请求停用”；按钮发送新 Turn 调用 `skills.disable`，不直接绕过审批。Disable 卡使用返回的精确 config version 提供“应用到当前会话”，应用后提供“重新启用”。
- 同一 Skill 的旧 Enable/Disable 卡在后续生命周期操作后标为 superseded，不能再应用过期配置版本。
- Registry 管理 API 增加 `/v1/skills/{skill_id}/disable` 并写 `skills.disable` operator audit。
- Skill Archive 增加活跃绑定保护：任一未归档 Agent 的当前配置仍启用该 Skill 时返回 409；历史 Agent config 和旧 Session 不阻止归档并继续可回放。

自动化验证：

- 工具 manifest/execution 测试覆盖 Disable 的 write risk、审批 metadata、执行上下文和响应。
- Service 测试覆盖只移除目标 binding、保留其他 binding、重复幂等、Enable/Disable 并发冲突及基于最新配置重试。
- HTTP 测试覆盖 Enable -> Disable、operator audit，以及绑定存在时 Archive 409、停用后 Archive 成功。

真实验收：

- 独立 Agent `agt_000146`、Environment `env_000165`、Session `sesn_000229` 使用 Skill `skl_000082` / version `sklv_000114`（`skill-disable-e2e-20260714` v1）完成 Enable -> Session v2 -> Disable -> Session v3 全链路。
- Enable 审批调用 `call_ty7gfbkynoddvwa9ura1pc4o` 发布 Agent config v2；Session 先保持旧版本，再由 `session.config_updated` seq 187 精确应用 v2。
- Workbench 的“请求停用”创建独立 Turn；Disable 调用 `call_tau8xy8wsc7rz0qzxwkacij5` 在 seq 197 请求审批、seq 198 由用户批准，seq 201 返回 `removed=true`、previous v2、new v3、Session v2 和 `requires_session_upgrade=true`。
- Disable 后 Agent v3 的 `skills.enabled=[]`，Session 在应用前仍固定 v2；点击“应用到当前会话”写入 `session.config_updated` seq 377，精确升级到 v3。卡片只保留“重新启用”，旧 Enable/Disable 卡不再提供陈旧操作。
- v3 上的普通验证 Turn 在 `runtime.skills_resolved` seq 381 返回 `skills=null`，证明目标 Skill 不再进入 Runtime 上下文。
- Agent 仍绑定 Skill 时 Archive 返回 409，并明确提示先停用；移除 binding 后同一 Archive 返回 200，Skill 状态变为 `archived`。
- Workbench 在 `1280x720` 与 `390x844` 的 document 均无横向溢出，可见交互控件无 viewport 越界；桌面和移动截图中停用状态、按钮和文本无重叠。当前浏览器控制接口不支持 console/pageerror 事件监听，因此本轮未把“控制台零告警”记为强校验结果。
- 验收后 Session 已归档，`inspect.json`、`enable.json`、`disable.json` 三个临时 Artifact 及 object ref `obj_000506/507/509` 已删除；Agent v3 保持空 binding。不可变 Skill package object ref `obj_000503/504` 按历史回放与 retention 规则保留。

## 2026-07-14：离线 inputs_schema 与 Workbench 参数控件

完成内容：

- Skill version manifest 新增不可变 `inputs_schema`，使用内存 JSON Schema Draft 2020-12 编译与 SHA-256 compiled cache，不进行运行时网络读取。
- Schema 只允许本地 fragment `$ref`，拒绝 `$id`、`$dynamicRef`、远程引用、开放 object 和 Secret 注解，并限制 Schema/inputs 的字节、深度、节点与属性数量。
- GitHub 与当前 Session Artifact ZIP 的 `SKILL.md` YAML front matter 可声明 `inputs_schema`；Preview/Install 保留该契约，原文件与标准 ZIP 导出不被重写。
- 版本发布、Marketplace 安装、`skills.enable` 和 Runtime Resolver 使用同一校验器；Enable 失败发生在 Agent config 发布前，历史无 Schema version 保持 object-only 兼容。
- 校验错误仅返回输入路径和 Schema keyword，不回显非法值。Skill inputs 明确为非敏感上下文参数，凭据继续通过托管环境变量提供。
- Workbench 已安装版本详情新增 typed controls：enum、boolean、integer/number、string、textarea，以及 object/array JSON 回退；支持 title、description、default、required 和基础范围约束。
- Workbench 提交精确 `version` 与转换后的 `inputs`；可选字段只有显式填写后才进入 binding，客户端错误不会发出 Enable 请求。

自动化验证：

- `internal/skills` 覆盖合法输入、脱敏错误、离线引用、Secret 注解、开放 object、legacy compatibility 和 Runtime Resolve。
- Marketplace parser 覆盖 GitHub/Artifact front matter manifest 保留；service/HTTP 覆盖非法 Enable 不发布 Agent config、合法输入冻结到 binding，以及非法 version Schema 返回 `400` 且不创建 version。
- `web-app/src/skillInputs.test.js` 覆盖 manifest 解析、控件映射、本地 `$ref`、默认值、可选字段省略、显式 `false`、数字/JSON 转换和逐字段错误。
- `go test ./...` 全仓通过，Workbench 58 个 Node tests 均通过；production build 与 `git diff --check` 通过。

真实验收：

- 临时 Skill `skl_000083` / version `sklv_000115` 从本地 inline source 发布 required enum、integer range、boolean、textarea 和 JSON array 契约，全程无 GitHub 或任意 URL 请求。
- 非法输入返回 `400` 和 `/style (enum)`，未回显测试值，Agent config version 保持不变；Workbench 客户端同时拦截无效 JSON 与缺失 required number。
- Workbench 合法提交发布 Agent config v4，binding 固定 v1，并保留 `style` string、`max_findings` integer、`include_tests` boolean、`filters` array 和 `notes` string 类型；Resolve Preview 状态为 `resolved` 且 rendered context 包含同一输入。
- `1280x720` 与 `390x844` 的 document、main、详情和表单无横向溢出，可见控件均位于 viewport 内。最后一次 production rebuild 后，浏览器 URL 策略阻止页面 reload，因此移动全宽提交按钮以生成 CSS 与 bundle 内容校验完成，未重复截图。
- 验收 Agent `agt_000150` 已恢复 `skills.enabled=[]` 的 config v5；Skill 与 Session `sesn_000234` 已归档。标准 package object refs `obj_000515/516` 按不可变版本回放和 retention 规则保留。

## 2026-07-14：Workbench Skills 生命周期管理

完成内容：

- 已安装列表同时读取 Agent 最新配置和当前 Session 固定配置，逐项展示 Agent version、Session version、待应用及“任务仍启用”状态。
- 版本详情并列展示两份完整 binding，包含 version、mode、priority 和 inputs；任一历史版本都可重新配置 typed inputs 后精确启用。
- Workbench 支持直接 Enable/Disable 管理操作。Disable 带确认，只移除 Agent 最新配置中的目标 binding；Agent 仍绑定时 Archive 保持禁用。
- Agent 与 Session 配置版本条提供精确应用入口，只在 Session 为 idle 且 Agent 版本更新时提交对应 `to_version`；成功后刷新 Session metadata、runtime config 和事件。
- Enable 对完全相同的 binding 返回 `changed=false`，不创建重复 Agent config version；inputs 使用结构化 JSON 语义比较，不受对象键顺序影响，空值与空对象等价。
- 首次 Enable 返回 `changed=true`；管理 HTTP endpoint 在实际发布时返回 `201`，幂等请求返回 `200`，operator audit 同步记录 `changed`。
- 每次成功 Enable/Disable 都重新拉取 Agent metadata，即使服务端判定幂等，也能消除其他客户端先修改配置造成的工作台陈旧状态。
- 无活动 Session 时不读取残留 runtime config 作为 Session binding；整个管理闭环只调用本地 TMA API，不新增公网请求。

自动化验证：

- Service/HTTP 测试覆盖首次 `changed=true`、重复 Enable 不增版本、重排 inputs 键序仍幂等、Disable 及 operator audit。
- `skillInputs.test.js` 覆盖完整 binding 解析、inputs 键序无关比较、表单回填、停用后 Session 仍启用状态和精确 Session 目标版本。
- Skills 聚焦 Go 测试通过；Workbench 70 个 Node tests 通过。

真实验收：

- 使用独立 Agent `agt_000152`、Environment `env_000171`、Session `sesn_000236` 和离线 Skill `skl_000084`（`lifecycle-e2e-20260714130826`）完成 v1/v2 不可变版本生命周期；安装与管理流程只访问本地 TMA API。
- 首次 Enable v1 返回 `201`、`changed=true`，Agent config 从 #1 发布到 #2；相同 binding 和仅调整 inputs 对象键顺序的请求均返回 `200`、`changed=false`，配置保持 #2。切换到 v2 并修改 mode/priority/inputs 后只发布 config #3。
- Workbench 将 idle Session 从 #1 精确应用到 #3；停用后 Agent 发布 #4、Session 仍为 #3，列表和详情同时显示“任务仍启用”。再次应用 #4 后 Agent、Session 和 runtime config 均为 `skills.enabled=[]`。
- 重复 Disable 返回 `201`、`removed=false`，`previous_config_version` 与 `new_config_version` 均为 #4；没有生成空的 config #5。
- `1280x720` 与 `390x844` 均无 document 横向溢出或可见内容越界；双 binding、typed controls、停用确认和中间状态无重叠，页面 console error/warning 为空。Browser client 自身的 Statsig 外网请求失败不属于 TMA 页面请求，未影响验收。
- 验收后 Skill 与 Session 已归档，Agent 保持 config #4 和空 bindings。v1/v2 的 `SKILL.md` 与 ZIP 对象引用 `obj_000544` 至 `obj_000547` 保留，归档后两个 package 下载仍返回 `200`。

## 2026-07-14：Skills 内部市场管理

完成内容：

- 新增 `skill_marketplace_entries` 与 `sment_*` ID，市场条目冻结引用 workspace 内精确 `skill_id + skill_version`，不复制标准 package 或 object refs。
- 生命周期固定为 `draft -> pending_review -> published -> withdrawn`；只允许单向逐级推进，同状态操作幂等，跨级、回退和审核后编辑返回 conflict。
- 草稿支持摘要、分类和最多 12 个标签；提交时重新确认 Skill 仍为 active，归档 Skill 不能进入审核或发布。
- 同一 workspace 的同一 Skill 同时最多一个已发布 version；旧版下架后可继续发布已审核的新版本。下架不归档 Skill，也不删除不可变 versions/package objects。
- 新增创建、列表、详情、草稿更新、提交、发布和下架 control API；所有写操作记录成功或失败的 operator audit，并固定 workspace 边界。
- Workbench Skills 增加“市场管理”视图，提供四态统计筛选、条目列表、稳定状态流水线、精确版本草稿、审核意见、下架确认和终态说明。

自动化验证：

- `TestMarketplaceEntryHTTPLifecycle` 覆盖 control auth、精确版本草稿、非法跨级、审核后禁止编辑、单发布版本约束、新旧版本切换及 operator audit。
- `TestPostgresSkillMarketplaceEntryLifecycle` 在真实 PostgreSQL 通过，验证数据库唯一约束和完整四态持久化。
- `marketplaceCatalog.test.js` 固定四态顺序、唯一下一动作、状态标签和进度显示；Workbench 76 个 Node tests 与 production build 通过。

真实验收：

- 离线 Skill `skl_000086` / version `sklv_000120`（`market-admin-e2e-20260714141920`）通过 Workbench 创建市场条目 `sment_000003`，完整执行草稿保存、提交审核、填写审核意见并发布、填写下架原因和二次确认下架。
- 草稿精确固定 v1，标签 `review/internal/quality` 在保存和重新加载后保持三个独立值；提交后展示字段全部只读。审核意见和下架原因在最终 API 响应中精确保留。
- `1280x720` 无 document 横向溢出，列表、编辑器、四态进度和表单无重叠；`390x844` 为 `documentOverflow=false`、`visibleOverflow=[]`、`emptyButtons=0`，下架确认与终态布局正常。移动 Skills header 修正为 322px 满宽内容轨道。
- 验收清理删除临时市场条目并归档 Skill，默认市场列表恢复 0 条；create/update/submit/publish/withdraw 五条 operator audit 保留。不可变对象 `obj_000553/554` 保留，归档后 package 下载返回 `200`。

## 2026-07-14：Skills 内部市场消费闭环

完成内容：

- Marketplace 默认来源改为内部市场，支持同 Organization 跨 Workspace browse、query/category/tag 筛选、Preview、Install 和后续升级；全流程只读取 PostgreSQL 与配置的对象存储。
- Catalog Preview 精确读取发布方 `tma.skill-package.v1` ZIP，复核发布 workspace、size、SHA-256、version checksum 和 package format，并复用现有 Policy、attestation、静态扫描、builtin binary scan、SBOM、审批和审计。
- consumer provenance 固定为 `source_type=catalog`、`source_locator=<publisher skill_id>`、version `source_ref=<entry id>` 和 `source_revision=<ZIP SHA-256>`。下架阻止新消费，已安装 consumer Skill 保留。
- 新增 `000063_skill_catalog_sources.sql`；RLS 只开放同组织 published-only SELECT，写策略继续锁定当前 workspace。启动自检要求 catalog policies 存在。
- RBAC 明确为 operator 创建/编辑/提交，admin 发布/下架；viewer/member 可浏览，member 可 Preview/Install。
- Workbench Marketplace 增加“内部市场/精确仓库/关键词/离线 ZIP”四来源并默认内部市场。
- TMA `skills.discover` 默认查询内部 Catalog 并返回 `catalog_entry_id`；只有显式 `provider=github` 或精确 `repository` 才进入 GitHub Client，`skills.preview/install` schema 已支持 catalog source。

自动化验证：

- 受限 PostgreSQL runtime role（非 superuser、无 BYPASSRLS）验证同组织可见、跨组织不可见、发布中禁止归档和下架后隐藏；首次测试发现并修复 catalog RLS 策略递归。
- `TestPostgresInternalMarketplaceHTTPPreviewAndInstallLocalFS` 使用真实 PostgreSQL 与临时 LocalFS 对象存储完成 Discover -> Browse -> Preview -> Install，并核对 consumer 持久化 provenance；测试不访问公网。
- HTTP 内存测试覆盖下架后 Preview 404；工具测试覆盖 catalog 默认 Discover、GitHub 显式兼容和 catalog Preview/Install schema。
- `go test ./...`、Workbench 76 个 Node tests、production build 和 `git diff --check` 通过。

真实验收：

- 发布方 Workspace `wksp_market_pub_20260714162240` 的 Skill `skl_000091` / version `sklv_000125` 以条目 `sment_000006` 发布；消费者 Workspace `wksp_market_consumer_20260714162240` 通过 Agent `agt_000156`、Environment `env_000176` 和 Session `sesn_000241` 完成内部市场消费。
- Workbench 默认展示内部市场和 1 个候选；安全预览返回 `new_install`、MIT、1 个 asset、0 binary、0 findings、builtin 扫描和 server Policy。安装后创建 consumer Skill `skl_000092` / version `sklv_000126`，再次预览返回 `unchanged` 和禁用的“已是最新”。
- consumer provenance 为 `catalog / skl_000091 / SKILL.md`，version `source_ref=sment_000006`、`source_revision=825a0dcb238f29ae6db32782b1320449810fe9da0126b442917a4a790854efdb`；整个浏览、预览和安装流程未选择 GitHub，不需要公网请求。
- `1280x720` 下四来源保持单行，`390x844` 下稳定为 2x2；两种 viewport 的 document 均无横向溢出，移动端长文本无可见越界，候选、Preview 安全区和操作按钮无重叠，页面 console logs 为空。
- 下架后 Discover 返回 0 个候选、Preview 返回 `404`，Workbench 刷新后显示“内部市场暂无匹配的已发布 Skill”；已安装 consumer Skill 仍保持 active，证明下架不破坏消费方不可变副本。
- 验收结束后条目保持 `withdrawn`，发布方与消费者 Skill 均已归档，Session 已终止；发布/消费 package object refs `obj_000589` 至 `obj_000594` 和审计记录按回放、追溯与 retention 规则保留。

## 2026-07-14：Skills 内部市场版本升级

完成内容：

- 内部市场 Browse 候选按 consumer registry 和最新不可变 version 返回 `new_install / upgrade / unchanged / blocked`，并携带已有 Skill ID、version、entry source ref 和 ZIP revision。
- Workbench 候选使用“可安装 / 有新版本 / 已安装 / 不可安装”状态和对应操作；安装或升级成功后本地候选立即刷新，不依赖再次查询。
- Upgrade Preview 明确展示本地 vN、目标 vN+1、主文件变化及新增/修改/删除资产；升级时安装标识锁定，避免确认对象与实际写入 identifier 不一致。
- 第一次点击“升级版本”只显示确认条，不写 registry；确认条明确旧版继续保留，第二次“确认升级”才发送 `upgrade_existing=true`。
- 升级只追加 consumer 不可变 version，不自动启用或修改 Session。已安装详情继续列出全部历史版本，可下载精确 package 并重新启用任一版本。

自动化验证：

- `TestInternalMarketplaceHTTPBrowsePreviewAndInstall` 扩展为完整 v1 -> 安装 -> v2 发布 -> 候选 upgrade -> Preview diff -> 缺少 `upgrade_existing` 返回 409 -> 确认升级 -> v1/v2 并存 -> 候选 unchanged -> 下架隐藏。
- `marketplaceCatalog.test.js` 覆盖四种消费者状态的标签、tone、操作名，以及只为有效 upgrade 推导下一本地版本。
- `go test ./...`、84 项 Workbench tests、production build 和 `git diff --check` 通过。

真实验收：

- 发布方 `skl_000093` 的 v1/v2 分别通过 `sment_000007` 和 `sment_000008` 发布；消费者 Agent `agt_000159`、Environment `env_000180`、Session `sesn_000245` 从 Marketplace 安装到 `skl_000094`。
- v1 安装后候选为“已安装 / 本地 v1”；发布 v2 后变为“有新版本 / 查看更新”。Preview 返回主文件变化、`references/release-gate.md` 新增和 `references/checklist.md` 修改，Policy allowed、0 binary、0 findings。
- 第一次点击升级后 consumer 仍只有 v1；确认条显示“确认升级到 v2 / 当前 v1 将继续保留”。第二次确认创建 `sklv_000130` v2，原 `sklv_000128` v1 保持可下载并提供“启用 v1”。
- `1280x720` 和 `390x844` 均无 document 横向溢出；桌面确认条使用独立整行，移动按钮稳定为双列且无文本换行，`visibleOverflow=[]`，页面 console logs 为空。
- 清理后 v2 市场候选为 0，发布方和消费者 Skill 已归档，Session 已终止；发布/消费两侧 v1/v2 对象 `obj_000602` 至 `obj_000615` 和审计记录保留。
