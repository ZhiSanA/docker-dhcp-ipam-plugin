# Docker DHCP IPAM 插件

一个 Docker IPAM 驱动插件，通过 DHCP 从宿主机局域网为容器分配 IP 地址。

配合 **macvlan** 网络使用——容器直接从局域网 DHCP 服务器获取 IP，无需 NAT，在物理网络上可路由。

## 工作原理

```
容器 → macvlan → DHCP IPAM 插件 → 局域网 DHCP 请求 → DHCP 服务器 → IP 租约
```

1. 插件启动时自动检测宿主机网卡和子网（从默认路由）
2. Docker 创建 macvlan 网络时，插件校验请求的子网是否与宿主机一致
3. 容器接入时，插件用稳定 MAC 向局域网 DHCP 服务器发起请求
4. 获取的 IP 返回给 Docker，后台 goroutine 自动续租

## 前提条件

- Docker CE（支持 managed plugin）
- 宿主机连接了有 DHCP 服务器的局域网
- `CAP_NET_RAW` 和 `CAP_NET_ADMIN` 权限（插件自带）

## 快速开始

### 1. 构建并安装插件

```bash
# 克隆仓库
git clone https://github.com/tuzi/docker-dhcp-ipam-plugin.git
cd docker-dhcp-ipam-plugin

# 编译二进制 → 构建 rootfs → 创建并启用 Docker managed plugin
# 插件名默认为 fox.zoo.twofactor.space/tuzi/docker-dhcp-ipam-plugin
./build.sh

# 也可指定自定义名称：
# ./build.sh my-registry/dhcp-ipam:latest
```

### 2. 创建 macvlan 网络

```bash
docker network create \
  --driver macvlan \
  --ipam-driver fox.zoo.twofactor.space/tuzi/docker-dhcp-ipam-plugin:latest \
  --ipam-opt subnet=192.168.1.0/24 \
  --opt parent=eth0 \
  my-network
```

**注意：** `--ipam-opt subnet` 必须与宿主机默认路由网卡的子网一致，不传则会报错。

### 3. 运行容器

```bash
docker run --rm --network my-network --name my-app nginx:alpine
```

容器会从局域网 DHCP 服务器获取 IP。

### 4. 验证

```bash
docker inspect my-network
docker exec my-app ip addr show eth0
```

## 配置

### 环境变量

| 变量 | 默认值 | 说明 |
|----------|---------|-------------|
| `DHCP_IPAM_INTERFACE` | 自动检测 | DHCP 使用的宿主机网卡。不填则从默认路由自动检测。 |
| `DHCP_IPAM_SOCKET_PATH` | `/run/docker/plugins/dhcp-ipam.sock` | Docker 插件通信的 Unix 套接字路径。 |
| `DHCP_IPAM_TIMEOUT` | `10s` | DHCP 请求超时时间（Go 时长格式，如 `5s`、`30s`）。 |
| `DHCP_IPAM_RETRIES` | `3` | DHCP 请求失败重试次数。 |
| `DHCP_IPAM_RENEW_INTERVAL` | `30s` | 租约续期检查间隔。 |
| `DHCP_IPAM_MAC_FROM_NAME` | `true` | 启用后，从容器的 `com.docker.network.endpoint.name` 生成稳定的 MAC 地址。这样重建容器（同名）会拿到相同的 DHCP 租约 IP。设为 `false` 则使用 Docker 分配的 MAC。 |

### 设置环境变量

```bash
# 指定自定义网卡
docker plugin set fox.zoo.twofactor.space/tuzi/docker-dhcp-ipam-plugin DHCP_IPAM_INTERFACE=eth1
```

或在构建前编辑 `config.json`，加到 `"env"` 数组：

```json
"env": [
  {"name": "DHCP_IPAM_INTERFACE", "value": "eth0"},
  {"name": "DHCP_IPAM_TIMEOUT", "value": "15s"}
]
```

## MAC 地址策略

插件按以下顺序确定 DHCP 请求使用的 MAC 地址：

1. **从容器的名称生成 MAC**（默认启用，`DHCP_IPAM_MAC_FROM_NAME=true`）—— 通过 FNV-32a 哈希从容器的 `com.docker.network.endpoint.name`（即 `--name` 指定的名称）生成一个固定 MAC。容器名不变，重建后 MAC 不变，DHCP 服务器就会分配同一个 IP。
2. **使用 Docker 分配的 MAC** —— 读取 `com.docker.network.endpoint.macaddress`。
3. **兜底哈希** —— 用 PoolID（CIDR 字符串）生成 MAC 作为最后手段。

## 文件结构

```
├── main.go              # 入口
├── internal/
│   ├── config.go        # 配置结构体、环境变量加载
│   ├── driver.go        # IPAM 驱动（RequestPool、RequestAddress、ReleasePool、ReleaseAddress）
│   ├── dhcp.go          # DHCP 客户端封装（Obtain、Renew、Release、RenewLoop）
│   ├── interface.go     # 宿主机网卡检测（/proc/net/route、net.Interface）
│   ├── store.go         # 内存 PoolStore 和 LeaseStore
│   └── mac.go           # MAC 地址解析和生成
├── config.json          # Docker managed plugin 清单
├── build.sh             # 构建脚本
├── docker-compose.yaml  # 测试用 compose 文件
└── go.mod / go.sum      # Go 模块依赖
```

## 常见问题

### 查看插件日志

```bash
journalctl -fu docker | grep dhcp-ipam
```

### "pool must be specified"

创建网络时必须传 `--ipam-opt subnet=...`，示例：

```bash
docker network create ... --ipam-opt subnet=192.168.1.0/24
```

### "requested pool X != detected subnet Y"

`--ipam-opt subnet` 指定的子网与宿主机检测到的子网不匹配。检查宿主机网卡：

```bash
ip route show default
ip addr show <接口名>
```

### "static address not supported"

插件只通过 DHCP 分配地址，不支持给单个容器指定静态 IP（网关地址由 Docker 自动处理）。

### 多个容器拿到相同 IP

确认 `DHCP_IPAM_MAC_FROM_NAME=true`（默认）且容器名称不同。同名容器会生成相同的 MAC，DHCP 返回相同 IP。

### 插件加载失败

检查插件是否有 `CAP_NET_RAW` 和 `CAP_NET_ADMIN` 权限：

```bash
docker plugin inspect <插件名>
```

## 从源码构建

```bash
# 编译独立二进制
go build -o docker-dhcp-ipam-plugin .

# 构建 Docker managed plugin
./build.sh
```

## 许可证

MIT
