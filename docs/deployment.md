# TMA 部署

## 拓扑

生产由反向代理/Ingress、`tma-server`、一个或多个 `tma-worker`、PostgreSQL、S3 兼容对象
存储、OIDC Provider 和可选 OTel/Prometheus 组成。Server 是无本地状态控制面；Worker 承担
本地系统、沙箱、浏览器和插件执行。

Server 与 Worker 使用不同 service token。迁移 owner、runtime DB role、对象存储凭据和
LLM key 必须分离。生产 Server 使用 `TMA_ENV=production`，启动时会校验认证、Worker token、
数据库 RLS 和必需配置。

## 构建与快速入口

仓库提供 Docker Compose、Kubernetes manifests 和 `scripts/deploy.sh`。先检查脚本帮助并准备
Secret，再执行目标环境部署：

```bash
scripts/deploy.sh --help
scripts/deploy.sh compose --env-file deploy/docker/.env.production
scripts/deploy.sh k8s --namespace tma
```

镜像应固定 immutable tag/digest。Worker 镜像只包含所需 runtime；需要 Docker/Browser 的
Worker 单独构建和授权，不把 Docker socket、浏览器凭据或主机目录交给通用 Server。

## 数据库迁移

新空库使用当前基线 `sql/baselines/000085_baseline.sql`；已有库只应用更高编号的
`sql/migrations/*.sql`。基线不是升级脚本，不能覆盖已有数据库。已部署的 migration 不删除、
重编号、修改或 squash。

```bash
psql "$TMA_MIGRATION_DATABASE_URL" \
  -v ON_ERROR_STOP=1 \
  --single-transaction \
  -f sql/baselines/000085_baseline.sql
```

Migration owner 需要 DDL 权限，应用 runtime role 只获得必要 DML/sequence 权限，并且不是
owner、superuser 或 `BYPASSRLS`。生产 migration tool 应维护 version ledger 和 advisory lock，
不能由多个应用副本并发运行 `make migrate-up`。

验证：

```bash
make generate-sql-baseline
git diff --exit-code -- sql/baselines/000085_baseline.sql
TMA_POSTGRES_TEST_PORT=55432 make verify-sql-baseline
```

## Docker Compose

1. 在 Secret Manager 或受限 env file 配置数据库、OIDC、Worker、对象存储和加密 secret。
2. 先运行一次 migrate job，成功后撤销 owner Secret。
3. 启动 PostgreSQL/对象存储依赖，再启动 Server，最后启动 Worker。
4. 只通过反向代理暴露 Server；数据库、对象存储管理端和 Worker 不暴露公网。
5. 设置 health check、restart policy、volume backup 和日志轮转。

Compose 适合单机或小规模环境。多 Worker、滚动升级和故障域隔离使用 Kubernetes。

## Kubernetes

1. 创建 Namespace、NetworkPolicy、ServiceAccount、Secret 和 PVC/外部服务引用。
2. 运行 migration Job 并等待 `Complete`。
3. 部署 Server Deployment/Service/Ingress，再部署按能力拆分的 Worker。
4. 设置 requests/limits、PodDisruptionBudget、topology spread 和 readiness/liveness probes。
5. 使用 HPA 前先验证 PostgreSQL、LLM、对象存储和 Worker queue 容量。

```bash
kubectl apply -f deploy/kubernetes/base/migration-job.yaml
kubectl -n tma wait --for=condition=complete job/tma-database-baseline-000085 --timeout=10m
kubectl -n tma logs job/tma-database-baseline-000085
```

NetworkPolicy 默认拒绝，只允许 Server 访问 PostgreSQL、对象存储、OIDC、LLM/MCP allowlist
和 OTel；Worker 根据能力单独开放网络。不要通过 privileged Pod 解决路径或设备权限问题。

## 对象存储

生产使用专用 Bucket、TLS、服务端加密、版本化、生命周期和备份。对象 metadata 与 PostgreSQL
引用需要一致恢复点。下载由鉴权 API 或短时签名 URL 提供，不能公开 Bucket。

文件系统 Provider 仅适合本机开发；多副本部署禁止依赖容器本地目录共享 Artifact。

## Onlyboxes / cloud sandbox

Onlyboxes 作为 `cloud_sandbox` Provider 接入，不改变 Tool contract。推荐独立 Worker/节点运行：

- 固定 Onlyboxes 和 runtime 版本。
- 为每个 Workspace/Session 创建隔离目录和生命周期。
- 默认禁网；按 Session policy 开放网络。
- 限制 CPU、内存、进程、磁盘、运行时间和输出。
- 不挂载 Server secret、宿主根目录或控制 socket。
- cleanup worker 回收过期 container/data，并保留审计 metadata。

部署前先在目标主机验证 runtime（Docker 或 boxlite）、镜像拉取、目录权限、网络策略、取消、
超时和崩溃清理。PoC 可单机，生产应把沙箱节点与控制面分离。

## 升级与回滚

1. 备份并验证 PostgreSQL 与对象存储恢复点。
2. 先部署向后兼容的 expand migration。
3. 滚动升级 Server，再按 capability 滚动 Worker。
4. 执行健康、OIDC、RLS、对象存储、Agent Turn、审批和 Artifact 验收。
5. 完成数据回填后再发布 contract migration。

应用只能回滚到兼容当前 schema 的镜像。数据库没有自动 down migration；不通过手工删列
回滚。不可兼容故障使用已验证恢复点。

## 上线检查

- [ ] `TMA_ENV=production` 且 OIDC/JWKS、audience、claim mapping 已验证。
- [ ] runtime DB role 非 owner/superuser/BYPASSRLS，Workspace RLS 强制启用。
- [ ] `000085` 基线或增量 migration 成功，应用不持有 owner 凭据。
- [ ] Server/Worker/control token 分离并来自 Secret Manager。
- [ ] S3 Bucket 已加密、版本化、备份且最小权限。
- [ ] Worker 按 Workspace/runtime 隔离，Server local-system fallback 关闭。
- [ ] NetworkPolicy、MCP/Web egress allowlist 和沙箱禁网默认值已验证。
- [ ] Prometheus/OTel、安全审计 outbox 和关键告警正常。
- [ ] 备份恢复、优雅重启、硬崩溃、lease fencing 在隔离环境演练通过。

配置见 [configuration.md](./configuration.md)，告警和调查见 [operations.md](./operations.md)。
