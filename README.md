# Docker DHCP IPAM 插件

一个 Docker IPAM 驱动插件，通过 DHCP 从宿主机局域网为容器分配 IP 地址。

配合 **macvlan** 网络使用——容器直接从局域网 DHCP 服务器获取 IP，无需 NAT，在物理网络上可路由。

## 工作原理

```
容器 → macvlan → DHCP IPAM 插件 → 局域网 DHCP 请求 → DHCP 服务器 → IP 租约
```

1. 插件启动时自动检测宿主机网卡、子网和网关（同时检测 IPv4 和 IPv6）
2. Docker 创建 macvlan 网络时，插件校验请求的子网是否与宿主机一致
3. 容器接入时：
   - **IPv4**：发送 DHCP 请求，携带容器名作为 hostname，后台 goroutine 自动续租
   - **IPv6**：优先尝试 DHCPv6，若局域网无 DHCPv6 服务器则自动降级为 EUI-64（从容器的 MAC 生成稳定地址）
4. 获取的 IP 返回给 Docker

## 前提条件

- Docker CE（支持 managed plugin）
- 宿主机连接了有 DHCP 服务器的局域网
- `CAP_NET_RAW` 和 `CAP_NET_ADMIN` 权限（插件自带）

## 快速开始

### 1. 安装插件

```bash
docker plugin install ghcr.io/ZhiSanA/docker-dhcp-ipam-plugin:latest
```

### 2. 创建 macvlan 网络

```bash
docker network create \
  --driver macvlan \
  --ipam-driver ghcr.io/ZhiSanA/docker-dhcp-ipam-plugin:latest \
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

### 5. 或用 docker-compose 快速验证

项目中附带了一个 `docker-compose.yaml`，安装插件后修改网卡名（`parent`）和子网配置，即可一键拉起测试：

```bash
# 先编辑 docker-compose.yaml，修改 parent 和 subnet 为你的环境
docker compose up -d
docker compose ps
docker exec dhcp-test-nginx ip addr show eth0
docker exec dhcp-test-caddy ip addr show eth0
# 完成后清理
docker compose down
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
| `DHCP_IPAM_SKIP_GATEWAY_CHECK` | `false` | 设为 `true` 时，网关地址不校验是否在子网 CIDR 范围内，直接返回。 |
| `DHCP_IPAM_DISABLE_DHCPV6` | `true` | 默认禁用 DHCPv6，IPv6 地址通过 EUI-64 从容器 MAC 生成（无需等待 DHCPv6 超时）。设为 `false` 开启 DHCPv6。 |

### 设置环境变量

```bash
# 指定自定义网卡
docker plugin set ghcr.io/ZhiSanA/docker-dhcp-ipam-plugin DHCP_IPAM_INTERFACE=eth1
```

或在构建前编辑 `config.json`，加到 `"env"` 数组：

```json
"env": [
  {"name": "DHCP_IPAM_INTERFACE", "value": "eth0"},
  {"name": "DHCP_IPAM_TIMEOUT", "value": "15s"}
]
```

## IPv4/IPv6 双栈支持

插件原生支持双栈。在 `docker-compose.yaml` 或 `docker network create` 中同时配置 IPv4 和 IPv6 子网即可。

当 `poolID`（CIDR）是 IPv6 地址时，插件自动走 **DHCPv6** 路径；若局域网无 DHCPv6 服务器响应，自动降级为 **EUI-64** 方式从容器的 MAC 生成稳定 IPv6 地址。

### docker-compose 双栈配置示例

```yaml
networks:
  dhcp-net:
    driver: macvlan
    enable_ipv6: true
    driver_opts:
      parent: ens18
    ipam:
      driver: ghcr.io/ZhiSanA/docker-dhcp-ipam-plugin:latest
      config:
        - subnet: "192.168.1.0/24"
          gateway: "192.168.1.1"
        - subnet: "2409:8a50:a70:2110::/64"
          gateway: "2409:8a50:a70:2110::1"
```

### docker network create 双栈

```bash
docker network create \
  --driver macvlan \
  --ipam-driver ghcr.io/ZhiSanA/docker-dhcp-ipam-plugin:latest \
  --ipam-opt subnet=192.168.1.0/24 \
  --ipam-opt subnet=2409:8a50:a70:2110::/64 \
  --opt parent=eth0 \
  my-network
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
│   └── mac.go           # MAC 解析、EUI-64 IPv6 地址生成
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

如果需要自行构建，可以克隆仓库后运行构建脚本：

```bash
git clone https://github.com/tuzi/docker-dhcp-ipam-plugin.git
cd docker-dhcp-ipam-plugin
./build.sh
```

构建脚本会编译二进制 → 创建 rootfs → 创建并启用 Docker managed plugin。也可指定自定义名称：

```bash
# ./build.sh my-registry/dhcp-ipam:latest
```

或仅编译独立二进制：

```bash
go build -o docker-dhcp-ipam-plugin .
```

## 许可证

MIT
