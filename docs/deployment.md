# TMA 生产部署说明书

本文面向全新生产环境，覆盖 Docker Compose 单机部署和 Kubernetes 部署。当前数据库基线版本为 `000080`。

## 1. 部署架构与边界

生产环境包含以下组件：

| 组件 | 是否必需 | 数据职责 |
| --- | --- | --- |
| `tma-server` | 必需 | API、Web App、Agent Runtime、Session/Turn 调度 |
| PostgreSQL 16 | 必需 | Agent、Session、权限、事件、配置和对象元数据 |
| S3 兼容对象存储 | 必需 | Artifact、Skill package、二进制资源、workspace snapshot |
| 企业 OIDC/JWKS | 必需 | 用户身份、Workspace 和角色映射 |
| `tma-worker` | 按执行模式 | `local_system` 工具和插件执行 |
| OTLP Collector / Prometheus | 推荐 | 审计日志、指标和告警 |

PostgreSQL 不保存大文件内容。不要使用容器本地文件系统代替生产对象存储，也不要在多个 Server 副本间共享 `localfs` 对象目录。

生产必须使用两个数据库角色：

- `tma_owner`：仅供初始化和迁移使用，拥有 DDL 权限。
- `tma_runtime`：仅供 Server 使用，必须是 `NOSUPERUSER NOBYPASSRLS`，不能拥有业务表。

## 2. 上线前准备

准备以下外部资源：

1. PostgreSQL 16 数据库，数据库名为 `tma`。
2. 一个私有 S3 Bucket，例如 `tma-artifacts`，启用服务端加密、版本控制和访问日志。
3. OIDC Client/API Audience。Access Token 至少包含 `sub`、角色和可映射的 Workspace。
4. OpenAI Chat Completions 兼容模型网关及 API Key。
5. HTTPS 域名，例如 `tma.example.com`。
6. 容器镜像仓库。生产镜像必须使用不可变版本或 digest，不能使用 `latest`。

生成密钥时使用密码管理系统。临时生成示例：

```bash
openssl rand -base64 48  # Worker token
openssl rand -base64 48  # Worker control token
openssl rand -base64 32  # Environment encryption key
openssl rand -base64 48  # OIDC web session secret, if enabled
```

所有 Server 副本和 Worker 必须使用同一个 `TMA_ENV_ENCRYPTION_KEY`。轮换该密钥前必须设计旧密钥兼容或完成数据重加密，不能直接替换。

## 2.1 快速部署入口

统一入口：

```bash
scripts/deploy.sh docker --help
scripts/deploy.sh k8s --help
```

脚本会在实际部署前检查必需命令、配置文件、模板占位符、镜像参数和生产域名。`replace-with-*`、`replace-me` 或 `example.com` 未替换时会直接失败。

Docker 快速部署：

```bash
cp deploy/docker/.env.production.example deploy/docker/.env.production
chmod 600 deploy/docker/.env.production
# 编辑 deploy/docker/.env.production

scripts/deploy.sh docker --dry-run
scripts/deploy.sh docker
```

脚本会验证 Compose、Docker socket GID，创建并授权 sandbox 目录，构建浏览器沙箱镜像，启动 PostgreSQL 和 Server，然后等待 `/health`。需要 Worker 时增加 `--with-worker`。

Kubernetes 快速部署：

```bash
cp deploy/kubernetes/config.production.env.example deploy/kubernetes/config.production.env
cp deploy/kubernetes/runtime-secrets.env.example deploy/kubernetes/runtime-secrets.env
cp deploy/kubernetes/migration-secrets.env.example deploy/kubernetes/migration-secrets.env
cp deploy/kubernetes/release.production.env.example deploy/kubernetes/release.production.env
chmod 600 deploy/kubernetes/runtime-secrets.env deploy/kubernetes/migration-secrets.env
# 编辑以上四个文件

scripts/deploy.sh k8s \
  --release-file deploy/kubernetes/release.production.env \
  --init-db \
  --dry-run

scripts/deploy.sh k8s \
  --release-file deploy/kubernetes/release.production.env \
  --init-db \
  --yes
```

`--init-db` 只在全新数据库首次部署时使用。脚本执行基线 Job 成功后默认删除 migration owner Secret；后续普通发布不传 `--init-db`，也不需要在部署主机保留 `migration-secrets.env`。

## 3. 构建生产镜像

仓库根目录的 `Dockerfile` 提供以下 target：

| Target | 用途 |
| --- | --- |
| `server` | 无 Docker daemon 权限的 Server，适合 Kubernetes |
| `server-docker` | 包含 Docker CLI，适合专用 Docker 主机的 `cloud_sandbox` |
| `worker` | Worker，内置 Bash、curl、Git、jq、Python 3 |
| `browser-extension-worker` | Worker + `browser.*` Process Plugin |
| `migrate` | PostgreSQL 客户端、`000080` 基线和 runtime grants |
| `cli` | TMA CLI |

构建并推送：

```bash
export REGISTRY=registry.example.com/tma
export VERSION=0.1.0

docker build --target server -t "$REGISTRY/tma-server:$VERSION" .
docker build --target server-docker -t "$REGISTRY/tma-server-docker:$VERSION" .
docker build --target worker -t "$REGISTRY/tma-worker:$VERSION" .
docker build --target browser-extension-worker -t "$REGISTRY/tma-browser-worker:$VERSION" .
docker build --target migrate -t "$REGISTRY/tma-migrate:$VERSION" .
docker build -f extensions/browser-gateway/Dockerfile -t "$REGISTRY/tma-browser-gateway:$VERSION" extensions/browser-gateway

docker push "$REGISTRY/tma-server:$VERSION"
docker push "$REGISTRY/tma-server-docker:$VERSION"
docker push "$REGISTRY/tma-worker:$VERSION"
docker push "$REGISTRY/tma-browser-worker:$VERSION"
docker push "$REGISTRY/tma-migrate:$VERSION"
docker push "$REGISTRY/tma-browser-gateway:$VERSION"
```

无法访问 `proxy.golang.org` 的企业网络通过 build arg 使用内部 Go module proxy：

```bash
docker build --build-arg GOPROXY=https://goproxy.example.com,direct \
  --target server -t "$REGISTRY/tma-server:$VERSION" .
```

多架构发布可使用：

```bash
docker buildx build --platform linux/amd64,linux/arm64 \
  --target server \
  -t "$REGISTRY/tma-server:$VERSION" \
  --push .
```

发布前记录镜像 digest、Git commit、基线 SHA-256 和配置版本。

## 4. 初始化数据库

### 4.1 创建角色和数据库

使用 PostgreSQL 管理账号执行：

```sql
CREATE ROLE tma_owner LOGIN PASSWORD 'OWNER_PASSWORD'
  NOSUPERUSER NOBYPASSRLS NOINHERIT;

CREATE DATABASE tma OWNER tma_owner;

CREATE ROLE tma_runtime LOGIN PASSWORD 'RUNTIME_PASSWORD'
  NOSUPERUSER NOBYPASSRLS NOINHERIT;
```

### 4.2 应用全量基线

连接必须使用 `tma_owner`：

```bash
export TMA_MIGRATION_DATABASE_URL='postgres://tma_owner:OWNER_PASSWORD@postgres.example.com:5432/tma?sslmode=require'

psql "$TMA_MIGRATION_DATABASE_URL" \
  -v ON_ERROR_STOP=1 \
  --single-transaction \
  -f sql/baselines/000080_baseline.sql

psql "$TMA_MIGRATION_DATABASE_URL" \
  -v ON_ERROR_STOP=1 \
  -f deploy/postgres/runtime-grants.sql
```

基线只能用于空数据库。不能对已有 TMA 表或数据的数据库再次执行。

### 4.3 验证 runtime role

```bash
psql 'postgres://tma_runtime:RUNTIME_PASSWORD@postgres.example.com:5432/tma?sslmode=require' \
  -v ON_ERROR_STOP=1 \
  -c 'SELECT current_user, current_database();'
```

不要把 `TMA_MIGRATION_DATABASE_URL` 注入 Server 或 Worker。Server 只能获得 `tma_runtime` 连接串；生产启动时会主动拒绝 owner、superuser、`BYPASSRLS`、缺少 FORCE RLS 或权限不完整的数据库连接。

## 5. 对象存储

预先创建 Bucket，并给 TMA Access Key 最小对象权限：

- 对指定 Bucket 执行 `GetObject`、`PutObject`、`DeleteObject`。
- 如平台需要，允许列举指定前缀，但不要授予其他 Bucket 权限。
- 不向浏览器暴露 S3 凭据、Bucket key 或内部 endpoint。
- 不配置会误删活动 Skill、Artifact 或 snapshot 的通用生命周期规则。

AWS S3 示例：

```env
TMA_OBJECT_STORAGE_PROVIDER=s3
TMA_OBJECT_STORAGE_ENDPOINT=https://s3.us-east-1.amazonaws.com
TMA_OBJECT_STORAGE_REGION=us-east-1
TMA_OBJECT_STORAGE_BUCKET=tma-artifacts
TMA_OBJECT_STORAGE_USE_PATH_STYLE=false
```

MinIO、RustFS 等通常使用内部 HTTPS endpoint 并设置 `TMA_OBJECT_STORAGE_USE_PATH_STYLE=true`。

## 6. OIDC 配置

推荐使用 OIDC Discovery + JWKS：

```env
TMA_AUTH_MODE=oidc
TMA_AUTH_OIDC_ISSUER=https://identity.example.com
TMA_AUTH_OIDC_AUDIENCE=tma-api
TMA_AUTH_OIDC_SIGNING_ALGS=RS256,ES256
TMA_AUTH_OIDC_CLAIM_MAPPING_JSON={"subject_claim":"sub","roles_claim":"roles","workspace_claim":"workspace_id","owner_claim":"sub","allowed_workspace_ids":["wksp_default"]}
```

生产 Claim mapping 必须通过 `allowed_workspace_ids` 或 `group_mappings` 限制 Workspace。角色映射后的有效角色为 `viewer`、`member`、`operator`、`admin`。

如果启用内置浏览器登录，回调地址必须与 IdP 完全一致：

```env
TMA_AUTH_OIDC_WEB_LOGIN_ENABLED=true
TMA_AUTH_OIDC_WEB_CLIENT_ID=tma-web
TMA_AUTH_OIDC_WEB_REDIRECT_URL=https://tma.example.com/auth/callback
TMA_AUTH_OIDC_WEB_POST_LOGOUT_URL=https://tma.example.com/app
TMA_AUTH_OIDC_WEB_SESSION_SECRET=AT_LEAST_32_RANDOM_BYTES
```

## 7. Docker Compose 部署

Docker 路径适合单台专用 Linux 主机。Compose 示例包含 PostgreSQL、Server 和可选 Worker；对象存储和 OIDC 使用外部企业服务。

### 7.1 主机准备

```bash
sudo install -d -o 10001 -g 10001 -m 0700 \
  /var/lib/tma/workspaces \
  /var/lib/tma/data

getent group docker
stat -c '%g' /var/run/docker.sock
```

把 Docker socket 的 GID 写入 `TMA_DOCKER_GID`。`server-docker` 对 Docker daemon 的访问等价于主机 root 权限，因此必须使用专用执行主机，不能和其他租户工作负载混部。

### 7.2 创建配置

```bash
cp deploy/docker/.env.production.example deploy/docker/.env.production
chmod 600 deploy/docker/.env.production
```

编辑全部 `replace-with-*` 值。数据库密码建议使用 URL-safe 字符；如果包含 `@`、`:`、`/` 等字符，必须在 URL 中 percent-encode。

### 7.3 首次启动

```bash
docker compose \
  --env-file deploy/docker/.env.production \
  -f deploy/docker/docker-compose.production.yml \
  up -d --build
```

PostgreSQL 官方镜像只会在空数据卷第一次启动时执行基线和 runtime role 脚本。已有 `postgres-data` 卷时不会重复初始化。

查看状态：

```bash
docker compose \
  --env-file deploy/docker/.env.production \
  -f deploy/docker/docker-compose.production.yml \
  ps

docker compose \
  --env-file deploy/docker/.env.production \
  -f deploy/docker/docker-compose.production.yml \
  logs -f server
```

默认只监听宿主机 `127.0.0.1:8080`。使用 Nginx、HAProxy 或企业网关终止 TLS，再代理到该地址。SSE 路由必须关闭响应缓冲，并把读超时设为至少一小时。

### 7.4 可选 Worker

`cloud_sandbox` 由 Server 通过 Docker daemon 创建 Session 容器，普通部署不需要 Worker。需要 `local_system` 或 Worker Plugin 时启动：

```bash
docker compose \
  --env-file deploy/docker/.env.production \
  -f deploy/docker/docker-compose.production.yml \
  --profile worker \
  up -d worker
```

### 7.5 停止与删除

停止服务但保留 PostgreSQL：

```bash
docker compose -f deploy/docker/docker-compose.production.yml down
```

不要在生产执行 `down -v`，它会删除 PostgreSQL 数据卷。

## 8. Kubernetes 部署

基础清单位于 `deploy/kubernetes/base`。默认架构：

- 2 个无特权 `tma-server` Pod。
- 2 个无特权 `tma-worker` Pod。
- 外部 PostgreSQL、S3、OIDC 和模型网关。
- Nginx Ingress TLS 入口。
- `local_system` 工具由 Worker Pod 执行，Server 不挂载 Docker socket。

### 8.1 修改清单

修改以下内容：

- `configmap.yaml`：域名、OIDC、Workspace、S3、模型。
- `server.yaml`、`worker.yaml`、`migration-job.yaml`：镜像和版本。
- `ingress.yaml`：域名、IngressClass 和 TLS Secret。

私有镜像仓库必须已配置到 Kubernetes 节点或 `tma-server`、`tma-worker` ServiceAccount；快速脚本不会接收或创建 registry password。

不要直接应用 `secret.example.yaml`。使用 External Secrets、Sealed Secrets 或企业 Secret Manager 创建同名 Secret。

### 8.2 创建 Namespace 和 Secret

```bash
kubectl apply -f deploy/kubernetes/base/namespace.yaml

kubectl -n tma create secret generic tma-runtime-secrets \
  --from-literal=TMA_DATABASE_URL='postgres://tma_runtime:RUNTIME_PASSWORD@postgres.example.com:5432/tma?sslmode=require' \
  --from-literal=TMA_WORKER_AUTH_TOKEN='WORKER_TOKEN' \
  --from-literal=TMA_WORKER_CONTROL_AUTH_TOKEN='CONTROL_TOKEN' \
  --from-literal=TMA_OBJECT_STORAGE_ACCESS_KEY='S3_ACCESS_KEY' \
  --from-literal=TMA_OBJECT_STORAGE_SECRET_KEY='S3_SECRET_KEY' \
  --from-literal=TMA_LLM_API_KEY='LLM_KEY' \
  --from-literal=TMA_ENV_ENCRYPTION_KEY='ENCRYPTION_KEY'

kubectl -n tma create secret generic tma-migration-secrets \
  --from-literal=TMA_MIGRATION_DATABASE_URL='postgres://tma_owner:OWNER_PASSWORD@postgres.example.com:5432/tma?sslmode=require'
```

Shell 历史可能记录明文。真实生产应从 Secret Manager 同步，而不是直接使用 `--from-literal`。

### 8.3 执行一次性基线 Job

先确认 `tma_runtime` 角色已经创建，再执行：

```bash
kubectl apply -f deploy/kubernetes/base/migration-job.yaml
kubectl -n tma wait --for=condition=complete job/tma-database-baseline-000080 --timeout=10m
kubectl -n tma logs job/tma-database-baseline-000080
```

Job 成功后限制或删除 migration owner Secret：

```bash
kubectl -n tma delete secret tma-migration-secrets
```

升级时重新创建 migration Secret，运行新版本 migration Job，成功后再次删除。不要让 Server Deployment 持有 owner 凭据。

### 8.4 部署 Server、Worker 和 Ingress

```bash
kubectl apply -k deploy/kubernetes/base
kubectl -n tma rollout status deployment/tma-server --timeout=10m
kubectl -n tma rollout status deployment/tma-worker --timeout=10m
kubectl -n tma get pods,service,ingress
```

如由 cert-manager 管理证书，给 Ingress 增加对应 issuer annotation；否则先创建 `tma-tls`：

```bash
kubectl -n tma create secret tls tma-tls --cert=tls.crt --key=tls.key
```

### 8.5 Kubernetes 扩缩容

Server 使用 PostgreSQL lease 协调 Turn，可横向扩容：

```bash
kubectl -n tma scale deployment/tma-server --replicas=4
kubectl -n tma scale deployment/tma-worker --replicas=6
```

Worker 并发还受单 Pod 的 `TMA_WORKER_CONCURRENCY` 限制。资源规划需同时观察 CPU、内存、Worker work backlog、Turn lease 和外部模型限流。

### 8.6 当前 Kubernetes 执行限制

当前 `cloud_sandbox` 实现调用本地 Docker daemon。基础 K8s 清单不会挂载 Docker socket，也不会部署 privileged Docker-in-Docker Sidecar。

因此默认 K8s 清单使用 Worker-backed `local_system`，存在以下差异：

- Worker Pod 的 `/workspace` 是临时 `emptyDir`，Pod 重建或任务落到另一 Worker 后不会保留本地文件。
- Artifact 仍可上传对象存储，但 Worker-backed Provider 当前不实现 Session workspace snapshot restore/checkpoint。
- 只依赖 `SKILL.md` 指令的 Skill 可使用；需要物化脚本或资源文件到 `/tma/skills/<skill_id>/<version>` 的 Skill 目前不应在该模式上线。
- 不具备 Docker Session 容器复用和每 Session 独立文件系统。
- 基础 Worker 镜像不包含 Chromium/Playwright；需要 Worker-backed browser tools 时必须构建经过审核的自定义 Worker 镜像。

如果生产必须具备 Skill 脚本物化、跨节点 workspace 恢复和 Session 级沙箱，当前可选方案是使用专用 Docker 执行节点部署 `server-docker`，或先实现 Kubernetes-native sandbox Provider。不要为了绕过限制给普通 Server Pod 挂宿主机容器运行时 socket。

## 9. 上线验收

### 9.1 健康检查

```bash
curl -fsS https://tma.example.com/health
```

预期：

```json
{"status":"ok","service":"tiggy-manage-agent"}
```

### 9.2 身份与租户

```bash
curl -fsS https://tma.example.com/v1/auth/me \
  -H "Authorization: Bearer $ACCESS_TOKEN"
```

确认 `subject_id`、`workspace_id`、`owner_id` 和角色符合 IdP 映射。必须额外使用另一个 Workspace Token 验证跨 Workspace 请求返回 `403`。

### 9.3 对象存储与 Skill

至少验证：

1. 上传并下载一个 Session Artifact。
2. 创建或安装一个 Skill package。
3. 下载 package 并核对 checksum。
4. Docker 路径执行一个带脚本的 Skill，确认 `/tma/skills/<skill_id>/<version>` 为只读。
5. Docker 路径在不同执行节点恢复同一 Session workspace snapshot。

### 9.4 Worker

```bash
kubectl -n tma logs deployment/tma-worker --tail=100
```

检查 Worker 已注册、心跳正常且没有持续 lease/retry 错误。

## 10. 监控与日志

- `/health` 是公开探针，只表示进程及启动检查已通过。
- `/metrics` 受统一身份保护；Prometheus 应使用专用 Bearer Token 或受信任网关身份。
- JSON 日志写到 stdout，由 Docker logging driver 或 Kubernetes 日志 Agent 收集。
- 加载 `deploy/prometheus/tma-security-alerts.yml`。
- 推荐配置 `TMA_SECURITY_AUDIT_OTLP_ENDPOINT`，生产必须使用 HTTPS 和 durable outbox。
- SIEM 按稳定 `event.id` 去重，因为审计投递语义是 at-least-once。

告警至少覆盖：

- 认证失败、跨 Scope 拒绝和 Operator 拒绝。
- PostgreSQL 连接耗尽、慢查询、磁盘和复制延迟。
- S3 上传/下载失败和容量增长。
- Worker 离线、work backlog、lease 过期和重试率。
- Turn 失败率、模型 429/5xx、上下文超限和 completion validation failure。
- 审计 outbox backlog、dead letter 和完整性 key 异常。

## 11. 备份与恢复

至少备份：

1. PostgreSQL 全量备份与 PITR/WAL。
2. S3 Bucket 版本和跨区域复制。
3. OIDC、Ingress、ConfigMap、Secret 引用和模型 Provider 配置。
4. 已发布镜像 digest、数据库基线和所有后续迁移。

PostgreSQL 与对象存储包含相互引用。制定恢复点时应尽量保持两者时间接近；恢复后运行 Artifact、Skill package 和 snapshot 抽样校验。不要只备份 PostgreSQL。

每季度执行一次恢复演练，记录 RPO、RTO 和无法恢复的对象引用。

## 12. 升级和回滚

升级顺序：

1. 备份 PostgreSQL 和对象存储。
2. 推送不可变新镜像。
3. 使用 migration owner 单实例执行新增 SQL。
4. 验证迁移成功。
5. 滚动升级 Server。
6. 滚动升级 Worker。
7. 执行健康、OIDC、租户隔离、对象存储和 Agent Run 验收。

`000080_baseline.sql` 只用于首次安装。后续版本从 `000081_*.sql` 开始，不能重新生成基线后覆盖已部署数据库。

应用回滚只能回滚到与当前数据库 Schema 兼容的镜像。数据库迁移没有自动 down 脚本；破坏性 Schema 变更必须使用 expand/contract 两阶段发布。发生不可兼容故障时，从经过验证的 PostgreSQL + S3 恢复点恢复，而不是手工删列或回滚基线。

## 13. 生产检查清单

- [ ] Server 使用 `TMA_ENV=production`。
- [ ] OIDC issuer、audience、Claim mapping 和 Workspace allowlist 已验证。
- [ ] runtime 数据库角色不是 owner、superuser 或 `BYPASSRLS`。
- [ ] `000080` 基线与 runtime grants 成功。
- [ ] Server/Worker 未持有 migration owner 凭据。
- [ ] S3 Bucket 已加密、版本化、备份并限制权限。
- [ ] Worker token、控制 token、LLM key 和加密 key 来自 Secret Manager。
- [ ] HTTPS、SSE 超时和上传大小配置正确。
- [ ] Docker socket 仅存在于专用 Docker 执行主机；K8s Server 无 privileged/socket 权限。
- [ ] 跨 Workspace 访问测试返回 `403`。
- [ ] PostgreSQL + S3 联合恢复演练通过。
- [ ] 指标、日志、OTLP 审计和告警已接入。

完整环境变量说明见 [configuration.md](./configuration.md)，数据库基线规则见 [production-database-migrations.md](./production-database-migrations.md)，安全运维见 [security-operations.md](./security-operations.md)。
