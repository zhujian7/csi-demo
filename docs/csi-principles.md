# CSI 原理与流程

这份文档是 `csi-demo` 的伴生笔记，把 CSI 的模型、链路和坑位整理成一份可以独立阅读的参考。

读法建议：第 1 节是总纲，其余各节都可以从它推出来。看不懂某一节时，回到第 1 节问一句「字节现在对谁可见」，答案通常就有了。

文中标注「实测」的结论都在本仓库的实现上验证过，出处是 csi-demo 的实际调试过程，不是转抄规范。

---

## 1. 总纲：作用域模型

### 1.1 一个卷的生命，只有四个动作

```
 作用域    存储后端            一台节点                节点上一个目录          一个容器
           (别处)              (node)                 (staging)             (pod)
             │                   │                       │                    │
 动作    CreateVolume    ControllerPublish        NodeStageVolume     NodePublishVolume
             │                   │                       │                    │
 调用者   provisioner         attacher                kubelet              kubelet
         (看 PVC)      (看 VolumeAttachment)         (看 Pod)             (看 Pod)
```

从左到右，每一列问的都是「这个卷对谁可见」：

- `CreateVolume` —— 让卷**存在**（在存储后端）
- `ControllerPublishVolume`（attach）—— 让卷对**某台节点**可见
- `NodeStageVolume` —— 让卷**在这台节点可用**（mkfs、mount 到 staging path）
- `NodePublishVolume` —— 让卷**在这个容器里可见**（bind mount 到 target path）

作用域逐级收窄：存储后端 → 一台节点 → 节点上一个目录 → 一个容器。**每一步的消费者不同，所以实现它的组件也不同**——这是后面所有组件划分的源头。

### 1.2 一句话决定 90% 的设计

> **字节在哪里？**

只有两个答案：**在这台节点上**，或**在别处**。

| | 字节在别处 | 字节在本机 |
|---|---|---|
| 需要 attach | 是 | 否 |
| 需要拓扑（把卷 pin 在一台节点） | 是 | 否 |
| RWO 有强制者 | 有（attach 层） | 没有 |
| `NodeStageVolume` | 有实质工作（mkfs 等） | 视后端而定，可能无事可做 |
| 节点故障 | 卷要重新 attach | 重挂即在目标节点重新物化 |

「别处」⇒ 前两步不可省；「在本机」⇒ 前两步无事可做。**这不是可选的优化，是逻辑后果。**

### 1.3 前提约束

整条链路只受一条物理约束支配：

> **只有本节点的进程能写本节点磁盘。**

CSI 里所有看似绕的设计，追到底都是这一条在起作用。

---

## 2. 三个 service 与两种部署形态

### 2.1 service 的划分

| Service | 做什么 | 必须吗 |
|---|---|---|
| **Identity** | 自我介绍：我是谁、我会什么 | **必须**（每个实例都注册，见 2.3） |
| **Controller** | **集群级**动作：创建卷、attach | 可选 |
| **Node** | **节点级**动作：挂载 | Pod 要用就必须有 |

RPC 归属：

```
  RPC                         Service      谁调它                  跑在
  ────────────────────────    ──────────   ─────────────────────   ─────────────
  GetPluginInfo               Identity     csi-provisioner         controller
  GetPluginCapabilities       Identity     csi-provisioner         controller
  Probe                       Identity     kubelet                 node
  ────────────────────────    ──────────   ─────────────────────   ─────────────
  CreateVolume                Controller   csi-provisioner         controller
  DeleteVolume                Controller   csi-provisioner         controller
  ValidateVolumeCapabilities  Controller   csi-provisioner         controller
  ControllerGetCapabilities   Controller   csi-provisioner         controller
  ControllerPublishVolume     Controller   csi-attacher            controller
  ControllerUnpublishVolume   Controller   csi-attacher            controller
  ────────────────────────    ──────────   ─────────────────────   ─────────────
  NodeStageVolume             Node         kubelet                 node
  NodePublishVolume           Node         kubelet                 node
  NodeUnstageVolume           Node         kubelet                 node
  NodeUnpublishVolume         Node         kubelet                 node
  NodeGetInfo                 Node         node-driver-registrar   node
  NodeGetCapabilities         Node         kubelet                 node
```

### 2.2 部署形态，由作用域推出

- **Node 必须是 DaemonSet** —— 挂载是节点本地事实，只有本节点进程能写本节点磁盘。
- **Controller 应该是单份 Deployment** —— 它做的是集群级决策，多份会得出矛盾答案。

注意「**Controller service**」和「**Controller Deployment**」是两个轴上的东西：前者是协议分组（由 CSI 规范定），后者是进程布局（由部署约定定）。约定背后是硬理由，不是规范强制，但顺序不能反——**先有作用域，才有部署形态。**

**`--mode=all` 不是一种部署形态**，只是给 csi-sanity 用的：测试工具只连一个地址，三个 service 得挤在一个进程里。

### 2.3 一个二进制，三个 service

三个 service 不是三个部署。`csi-demo` 是一份代码，靠 `--mode` 决定注册哪几个：

```
              同一个二进制 cmd/csi-demo
        ┌────────────────────────────────────────────┐
        │  internal/driver/identity.go   → Identity   │
        │  internal/driver/controller.go → Controller │
        │  internal/driver/node.go       → Node       │
        └────────────────────────────────────────────┘
                      --mode 决定注册哪几个

   --mode=controller                        --mode=node
  ┌─ Pod (Deployment，全集群 1 份) ────┐   ┌─ Pod (DaemonSet，每台节点 1 份) ──┐
  │   ┌───────────────────────────┐   │   │   ┌──────────────────────────┐   │
  │   │ csi-demo                  │   │   │   │ csi-demo                 │   │
  │   │   Identity   ✓            │   │   │   │   Identity   ✓           │   │
  │   │   Controller ✓            │   │   │   │   Node       ✓           │   │
  │   │   Node       ✗            │   │   │   │   Controller ✗           │   │
  │   └────────────▲──────────────┘   │   │   └───────────▲──────────────┘   │
  │                │ unix socket      │   │               │ unix socket      │
  │   ┌────────────┴──────────────┐   │   │   ┌───────────┴──────────────┐   │
  │   │ csi-provisioner           │   │   │   │ node-driver-registrar    │   │
  │   │ csi-attacher              │   │   │   └──────────────────────────┘   │
  │   └───────────────────────────┘   │   │                                  │
  └───────────────────────────────────┘   └──────────────────────────────────┘
        ↑ 这两个是「客户端」，不是你写的   ↑ 同样是客户端
```

三个要点：

1. **Identity 两边都有。** 它不是某个部署，而是每个实例随身带的自我介绍。
2. **sidecar 是客户端，不是 service。** provisioner / attacher / registrar 都在**调你**。
3. sidecar 与 driver 共享一个 emptyDir 里的 unix socket。

### 2.4 最容易被忘的本质：driver 是被动的 gRPC server

driver **从不 watch API server**，只是坐着等调用。三条推论：

- 需要的信息都得由调用方**塞进参数里**（所以 `NodePublishVolume` 的 args 自带卷的一切）；
- 被动方必须**幂等**，因为重试是常态；
- **重启不会被任何人通知**（kubelet 会缓存连接与能力，继续按旧行为调用你）。

---

## 3. 谁触发谁：完整链路

### 3.1 核心：没有人「调用」sidecar

sidecar 和你的 driver 一样是**被动的**。区别在于 driver 只对 gRPC 请求做反应，而 sidecar 是 **controller**——它们自己 watch API server，看到对象变化就主动调你的 driver。

所以链路不是调用链，是**对象驱动链**：

```
  谁创建对象 ──► sidecar watch 到 ──► 调你的 driver ──► 结果写回另一个对象
```

### 3.2 完整链路（四列读法）

```
 ① 创建者            ② watcher / 调用者        ③ 你的 driver          ④ 产物
 ─────────────────────────────────────────────────────────────────────────────

 kubectl                                                  PV 对象
 apply pvc    ──►   csi-provisioner          ──►         （被创建）
                    watch 未绑定的 PVC                    PVC → Bound
                                         CreateVolume
                                         (Controller svc)

 kubectl              kube-controller-manager            VolumeAttachment
 apply pod    ──►     的 attachdetach ctrl   ──►         （被创建）
 (调度完成后)          ★ 不调用你，只建对象

                    csi-attacher             ──►         VA.status
              ──►  watch 到新的 VA          Controller   .attached = true
                                         PublishVolume
                                         (Controller svc)

 (kubelet)           ★ 直连，不经 sidecar   ──►         宿主机上出现
              ──►                          NodeStage     staging 挂载点
                                           Volume

                                           NodePublish   容器里出现
                                           Volume        target 挂载点
```

两个 ★：

- **attachdetach controller 不调用你的 driver**，它只负责**创建 `VolumeAttachment` 对象**。真正调 `ControllerPublishVolume` 的是看着这个对象的 csi-attacher。对象是中间人——这就是 attach 链路看起来绕的原因。
- **kubelet 不经过任何 sidecar**，挂载阶段它直连 node socket。

### 3.3 对象由谁创建

| 对象 | 谁创建 |
|---|---|
| `PVC` | 用户（或某个 operator） |
| `PV` | **csi-provisioner**（动态供给时它自己创建） |
| `VolumeAttachment` | **attachdetach controller**（住在 kube-controller-manager） |
| `CSINode` / 插件注册记录 | **node-driver-registrar** |
| `Pod` | 用户；**kube-scheduler** 决定落哪台节点 |

规律：**「创建对象的人」和「调用你的人」从来不是同一个**，中间隔着一个 K8s 对象。这不是绕，是解耦——正因为对象在中间，各组件才能互不知情、各自重启。

### 3.4 node-driver-registrar 是异类

它不 watch 任何东西，只在**启动时**做三件事：

```
  启动
   │
   ├─► 调 NodeGetInfo ──► driver 报出自己所在节点的 ID
   │
   ├─► 写 CSINode 对象 ──► 集群才知道「这台节点上有这个 driver」
   │
   └─► 把 socket 路径写进 /var/lib/kubelet/plugins_registry/
                          ▲
                          └── kubelet 从这里「发现」你的 driver
```

之后它基本闲着——**挂载时不经过它**。kubelet 靠这条注册记录找到 socket 后直连。

### 3.5 收尾的心智模型：两个控制面

```
┌─ kube-controller-manager（控制面，全集群一份）─────────────────────┐
│   PersistentVolumeController ──► （CSI 走外部 provisioner）        │
│   attachdetach controller    ──► 创建 VolumeAttachment             │
│   （scheduler 决定 Pod 落哪台节点）                                 │
└────────────────────────────────────────────────────────────────────┘
                   │ 创建对象
                   ▼
            ┌─────────────┐
            │ API Server  │  ← 所有对象的中转站
            └─────────────┘
                   │ 对象变化
        ┌──────────┼──────────────┐
        ▼          ▼              ▼
  csi-provisioner  csi-attacher  （kubelet 不看对象，它被 Pod 触发）
        │              │              │
        └──────────────┴──────────────┘
                       │ 都走 gRPC
                       ▼
              ┌─────────────────┐
              │  你的 csi-demo   │
              └─────────────────┘
              ▲
              └── kubelet 直连（node 侧）

┌─ 每台节点的 kubelet（节点面，每台一份）────────────────────────────┐
│   看到调度到本节点的 Pod ──► NodeStageVolume / NodePublishVolume   │
└────────────────────────────────────────────────────────────────────┘
```

---

## 4. attach：`VolumeAttachment` 到底是什么

### 4.1 它解决的问题

回到「字节在别处」。一块云盘同一时刻只能接在一台节点上。现在两边同时发生：

```
 node-1 的 kubelet: "我有个 Pod 要卷 X，给我接上"
 node-2 的 kubelet: "我有个 Pod 也要卷 X，也给我接上"
```

若各自去调云 API，就是竞态：设备可能被接到两台机器，两边都 `mkfs`，文件系统销毁。**这是脑裂。**

要避免它，attach 必须**串行化**，并且必须有人**记住**「卷 X 现在接在 node-1 上」。记在哪？——**记在 API server 里的一个对象里**。那个对象就是 `VolumeAttachment`。

类比：图书馆的借阅登记簿。想看同一本书的人有好几个，书架上只有一本。大家不直接抢书，而是看登记簿——它是唯一权威。

### 4.2 它长什么样

```yaml
apiVersion: storage.k8s.io/v1
kind: VolumeAttachment
metadata:
  name: csi-<哈希>                       # 见 4.5
spec:
  attacher: csi.demo.cubestack.io
  nodeName: csi-demo-worker              # ← "接给谁"
  source:
    persistentVolumeName: pvc-dd1193...  # ← "哪个卷"
status:
  attached: true                         # ← "接好了没有"
  attachError: <none>
```

**它只回答一个问题：某个卷，现在是不是接在某台节点上。** 不含任何存储实现细节（没有设备路径、没有云盘 ID）——它是**声明**，不是操作记录。

### 4.3 三方分工

| 角色 | 干什么 | **不**干什么 |
|---|---|---|
| **attachdetach controller**<br>(kube-controller-manager) | 做**决策**：Pod 落哪台？卷是不是 RWO？有没有已经接在别处？决定后**创建/删除** VA 对象 | **不调用你的 driver**，它不懂存储 |
| **csi-attacher**<br>(sidecar) | **看着** VA。看到新的 → 调 `ControllerPublishVolume`；成功后写 `status.attached: true` | **不决定**要不要 attach |
| **你的 driver** | 真正去「接」 | 不关心是谁、为什么 |

为什么拆成三个：**kube-controller-manager 是通用的**，对你的存储一无所知。它只能表达**策略**（「这个卷该接给这台节点」），落成一个对象；「具体怎么接」是你的 sidecar 的事。**对象是这两者之间的接口。**

注意 attach 的**决策**与**执行**分离在两端：决策在控制面、执行在 driver，中间靠对象传递。这与 `CreateVolume` 链路的形态不同（那边 provisioner 既决策又执行）。

### 4.4 一次完整的生命周期

```
 t0   用户创建 Pod，scheduler 决定落到 worker
       │
 t1   attachdetach controller 看到：
        「Pod 在 worker 上，要 PV-X；PV-X 是 RWO；还没接在别的节点」
       │
       │  ★ 决策在这里发生——唯一有全局视野的时刻
       ▼
 t2   创建 VolumeAttachment { nodeName: worker, source: PV-X }
       │                                    ← 登记簿上写了
       ▼
 t3   csi-attacher watch 到它
       │  调 ControllerPublishVolume(volume_id=..., node_id=worker)
       ▼
 t4   driver 返回成功
       ▼
 t5   csi-attacher 写 VA.status.attached = true      ← 已交付
       ▼
 t6   kubelet 看到「本节点关于 PV-X 的 VA 已 attached」
       │  ★ 在此之前它一直等；这是「锁已持有」的信号
       ▼
 t7   kubelet 调 NodeStageVolume → NodePublishVolume
       ▼
 t8   容器里能读写

 ─────────────── 用完 ───────────────

 t9   kubectl delete pod
       ▼
 t10  attachdetach controller 看到「没人再用 PV-X 了」→ **删除** VA
       ▼
 t11  csi-attacher 看到 VA 被删 → 调 ControllerUnpublishVolume
       ▼
 t12  卷空闲，可以被别的节点 attach
```

### 4.5 那个巨长的名字是算出来的

```
csi-<确定性哈希(attacher + 卷 + 节点)>
```

具体输入是实现细节，**关键是它确定**：同样的 driver + 同样的 PV + 同样的节点 ⇒ 必然同名。

**实测的副作用**：删 Pod 再建 Pod（落在同一节点）会删掉旧的 VA、再建出一个**同名**的 VA。attacher 记账时与删除抢跑，日志里会吐一串：

```
"Saving detach error" ... "Failed to save detach error to VolumeAttachment"
  err="volumeattachments... not found"
```

看着像 bug，其实是确定性命名 + 竞态，**终态是正确的**。

### 4.6 RWO 的强制者是谁

> **RWO 的强制者不是 kubelet，也不是 driver，而是「要不要创建这张 VA」这个决策。**

t2 创建 VA 的那一刻，这个卷就在集群语义上被占用了。别的节点想要同一个 RWO 卷，attachdetach controller 一看登记簿已经登记给别人，就报 `Multi-Attach Error`。

**实测对比**：`attachRequired: false` 时登记簿根本不存在，于是没有人为 RWO 背书——同一个 RWO PVC 的两个 Pod 被调度到两台节点、双双 `Running`，**各拿到一份互不相干的数据**（各自的 backing file、各自 `mkfs` 出的空文件系统），**没有任何报错**。不是 RWO 失灵了，而是执行它的角色从来没上岗。

---

## 5. 物理视图：字节怎么到达容器

### 5.1 三跳

以块设备后端（`--backend=loop`）为例：

```
 backing.img          /dev/loop3           /dev/loop3            staging path
 (普通文件)     ──►   (块设备)       ──►   (ext4 文件系统)  ──►   (挂载点)
  只有字节            还是只有字节          有结构了              能存文件了
        │                  │                    │                    │
        └── losetup ───────┴──── mkfs.ext4 ─────┴────── mount ──────┘
```

**mkfs 是「make filesystem」**：给一块裸设备写上超级块、inode 表、位图、目录项，把「第 N 个字节」翻译成「某个路径下的某个文件」。没有它，`mount` 报 `wrong fs type, bad option, bad superblock`。

**mkfs 是破坏性的**——它不是「没有才建」，而是「不管上面有什么都覆盖」。两道防线：

1. 先查挂载表早退（`IsMountPoint`）；
2. 用 `SafeFormatAndMount`：它先探测设备上有没有现成文件系统，**有就跳过格式化**。

第 2 条在真实世界尤其重要：**从快照恢复的卷本来就带文件系统**，无脑 mkfs 等于当场抹掉用户数据。所以生产 driver 一律用「先探测」的辅助，绝不直接调 `mkfs`。**「设备是新的还是已有内容」必须由探测决定，不能由调用次数决定。**

反例：`volumeMode: Block` 把裸设备直接给容器，不建文件系统，也就没有这一跳。

### 5.2 stage 与 publish 两层

```
   ┌────────────────┐   losetup    ┌───────────────┐
   │  backing.img   │─────────────►│  /dev/loop3   │
   └────────────────┘              └───────┬───────┘
                                           │  mkfs + mount
                           NodeStageVolume │  ← 一台节点上只做一次
                                           ▼
                      ┌────────────────────────────────────┐
                      │ /var/lib/kubelet/plugins/          │
                      │   kubernetes.io/csi/<drv>/<hash>/  │
                      │        globalmount                 │  staging path
                      └────────────────┬───────────────────┘
                                       │
                   NodePublishVolume   │  bind mount ← 每个 Pod 各做一次
                                       ▼
                ┌──────────────────────────────────────────┐
                │ /var/lib/kubelet/pods/<uid>/volumes/     │
                │      kubernetes.io~csi/<pv>/mount        │  target path
                └────────────────────┬─────────────────────┘
                                     ▼
                              容器里的 /data
```

这张图解释两件事：

1. **为什么要有 stage** —— 设备只 `mkfs` 和 mount 一次，N 个 Pod 从同一个挂载点 bind 出去。实测：同一个卷挂给两个 Pod，设备只被 mount 一次。
2. **两个挂载点的语义不同** —— staging 是「这个卷在这台节点上就绪」，target 是「这个 Pod 能用它了」。

### 5.3 `mountPropagation` 是命门

上面所有 mount 调用都发生在 **node plugin 容器自己的 mount namespace 里**，默认传不出去，宿主机和 kubelet 都看不见。

实测症状（缺 `Bidirectional` 时）：**容器内 `findmnt` 能看到 globalmount，宿主机 `/proc/mounts` 里没有。** 所以 node plugin 必须挂载 `/var/lib/kubelet/plugins` 并设：

```yaml
volumeMounts:
  - name: plugins-dir
    mountPath: /var/lib/kubelet/plugins
    mountPropagation: Bidirectional
```

这是所有生产 CSI driver 的标准做法。

---

## 6. 端到端实例：一块云盘的四个阶段

第 5 节讲「字节怎么到达容器」，但没有区分后端。这一节跟着一块云盘走完全程，重点看**哪些阶段根本没有 Linux 命令**。

### 6.1 场景

一块 20 GiB 云盘（EBS 型），PVC 已 Bound，Pod 被调度到 `worker-2`（云上实例 `i-0def456abc`）。

### 6.2 阶段 1：`CreateVolume` —— 没有 Linux 命令

```bash
aws ec2 create-volume \
  --availability-zone eu-west-1a \
  --size 20 --volume-type gp3
# → vol-0abc123def456
```

（真实 driver 用 SDK，这里用 CLI 只是为了看得见。）

**它在云端凭空造出一块盘，谁也没连上它。** 任何节点的内核都不知道它存在——`lsblk` 看不到。

`--availability-zone` 是必填的：**云盘诞生在一个可用区里，之后只能接给同可用区的实例。** 这就是「拓扑」的由来，也是 `WaitForFirstConsumer` 存在的理由——卷带着地理位置，而 Pod 落在哪还不知道。

### 6.3 阶段 2：`ControllerPublishVolume` —— 也没有 Linux 命令

```bash
aws ec2 attach-volume \
  --volume-id vol-0abc123def456 \
  --instance-id i-0def456abc \
  --device /dev/xvdf
```

同样是云端操作，**但这次产生了本地副作用**——`worker-2` 的内核自己把设备认了出来，**没有谁敲过任何命令**：

```bash
# 在 worker-2 上查看（这是自动发生的结果，不是 driver 敲的）
$ lsblk
NAME        MAJ:MIN RM  SIZE RO TYPE MOUNTPOINT
nvme0n1     259:0    0    8G  0 disk
└─nvme0n1p1 259:1    0    8G  0 part /
nvme2n1     259:5    0   20G  0 disk            ← 新来的，还没有文件系统

$ blkid /dev/nvme2n1
（无输出 —— 设备上什么都没有）
```

它之所以会自己出现，是因为这台实例的内核里本来就有 EBS / NVMe 客户端。**内核认识这个协议，就不需要用户态翻译**——与第 7 节同一原理，只是层次不同。

设备有两个名字，只能用稳定的那个：

```bash
# ✗ 编号取决于机器上已有几块盘，换台机器就变
/dev/nvme2n1

# ✓ 按卷 ID 索引的稳定路径
$ readlink -f /dev/disk/by-id/nvme-Amazon_Elastic_Block_Store_vol0abc123def456
/dev/nvme2n1
```

**`/dev/nvmeXn1` 是不稳定名字，`by-id` 才是稳定名字。** 这也是 `PublishContext` 存在的原因之一：**不是所有云 / 存储都提供 by-id 这类稳定映射**（Ceph RBD 就没有），所以通用做法是 controller 把设备信息随 `PublishContext` 递下来（见 4.3）。

### 6.4 阶段 3：`NodeStageVolume` —— 真的有命令了

在 `worker-2` 上，由 node plugin 容器执行：

```bash
# ① 定位设备（用稳定路径，并等它出现）
DEV=$(readlink -f /dev/disk/by-id/nvme-Amazon_Elastic_Block_Store_vol0abc123def456)

# ② 先探测有没有文件系统 —— SafeFormatAndMount 的第一步
blkid "$DEV"
#   无输出 ⇒ 新盘，需要格式化
#   有输出 ⇒ 已有文件系统，跳过 ③（从快照恢复的卷走这条路）

# ③ 格式化（只在 ② 为空时执行）
mkfs.ext4 -F "$DEV"

# ④ 建 staging 目录并挂载
STAGE=/var/lib/kubelet/plugins/kubernetes.io/csi/ebs.csi.aws.com/<hash>/globalmount
mkdir -p "$STAGE"
mount -t ext4 "$DEV" "$STAGE"
```

到此这台节点上有了可用的文件系统挂载点，但还没有容器能看到它。

### 6.5 阶段 4：`NodePublishVolume` —— bind mount

```bash
# ① 建 target 目录
TARGET=/var/lib/kubelet/pods/<pod-uid>/volumes/kubernetes.io~csi/<pv-name>/mount
mkdir -p "$TARGET"

# ② bind mount
mount --bind "$STAGE" "$TARGET"

# ③ 只读卷还要单独 remount
mount -o remount,bind,ro "$TARGET"
```

③ 是个真实的坑：`mount --bind -o ro` 在很多内核上不可靠，必须补一次 `remount,bind,ro`。所以 driver 里只读路径会多一步。

### 6.6 全图

```
        谁也没碰的云端
   ┌────────────────────────────────────────────┐
   │  vol-0abc123def456  (20GiB, gp3, eu-west-1a)      │
   └────────────────────────────────────────────┘
              ▲                          ▲
              │ CreateVolume             │ ControllerPublish
              │ (API，无命令)             │ (API，无命令)
              │                          │
      凭空造出一块盘              告诉云「把它接到 i-0def456abc」
              └──────────┬───────────────┘
                         │
                         │  ★ 到这里为止：0 条 Linux 命令
                         │
   ┌─ node worker-2 (i-0def456abc) ────────────────────────────┐
   │                                                           │
   │   /dev/nvme2n1  ← 内核自动认出来的，没人敲命令             │
   │        │                                                  │
   │        │ NodeStageVolume                                  │
   │        │   blkid（探测）→ mkfs.ext4（必要时）→ mount        │
   │        ▼                                                  │
   │   .../plugins/kubernetes.io/csi/.../globalmount           │
   │        │                                                  │
   │        │ NodePublishVolume                                │
   │        │   mkdir → mount --bind → (remount,bind,ro)       │
   │        ▼                                                  │
   │   /var/lib/kubelet/pods/<uid>/volumes/.../mount           │
   └───────────────────────────────────────────────────────────┘
                         │
                         ▼
                   容器里的 /data
```

### 6.7 汇总

| 阶段 | 在哪执行 | 谁调它 | Linux 命令 | 关键动作 |
|---|---|---|---|---|
| `CreateVolume` | **云端** | csi-provisioner | **无** | `create-volume`（需指定 AZ） |
| `ControllerPublishVolume` | **云端** | csi-attacher | **无**（内核会自动生成设备节点） | `attach-volume` |
| `NodeStageVolume` | **节点** | kubelet | **有** | `blkid` → `mkfs.ext4` → `mount` |
| `NodePublishVolume` | **节点** | kubelet | **有** | `mkdir` → `mount --bind` → `remount,ro` |

**两个阶段完全没有 Linux 命令，两个阶段全是 Linux 命令**，分界线正好是「云端 vs 节点」，也就是 Controller service 与 Node service 的分界线。这不是巧合：**Controller 那侧的逻辑是「跟云打交道」，Node 那侧是「跟本地内核打交道」。**

### 6.8 反向拆除

```bash
# NodeUnpublishVolume
umount /var/lib/kubelet/pods/<uid>/volumes/kubernetes.io~csi/<pv>/mount
rmdir  /var/lib/kubelet/pods/<uid>/volumes/kubernetes.io~csi/<pv>/mount

# NodeUnstageVolume
umount /var/lib/kubelet/plugins/kubernetes.io/csi/ebs.csi.aws.com/<hash>/globalmount

# ControllerUnpublishVolume
aws ec2 detach-volume --volume-id vol-0abc123def456

# DeleteVolume
aws ec2 delete-volume --volume-id vol-0abc123def456
```

注意 **Node 侧先拆、Controller 侧后拆**——挂载是叠上去的，拆必须从最上层开始。这里也能看出前面那条原理的实际后果：`NodeUnstageVolume` 只 umount，**不擦数据**；数据要不要删是 `DeleteVolume` 的事。

### 6.9 三个细节

1. **`/dev/nvme2n1` 是不稳定名字，`/dev/disk/by-id/...` 才是稳定名字。** 设备编号取决于机器上已有几块盘，换台机器就变。凡是靠猜设备名的地方都会在某台机器上翻车。
2. **`mkfs` 之前必须 `blkid`。** 格式化是破坏性的，而「设备上有没有东西」只能探测得知，不能靠调用次数推断——从快照恢复的卷上来就带着用户数据。
3. **`mount --bind` 的只读要单独 `remount`。** 一次 `--bind -o ro` 不可靠，这是内核的历史行为。

还有一个前提：这些命令都在 node plugin 容器里执行，而结果必须让宿主机可见——见 5.3 的 `mountPropagation`。缺了它，命令**全部成功**，宿主机却什么都看不到。

---

## 7. 后端的分类学：拿到任何存储，怎么推 driver 形态

### 6.1 关键问题不是「要不要挂载到节点」

**容器只能看见节点内核给出的文件系统视图**，所以最终落点必然是节点上的一条路径。真正的问题是：

> **内核有没有这个存储协议的原生客户端？**

有，一条 `mount` 就够了；没有，必须有人在中间翻译，而「谁翻译」决定了 driver 的形态。

### 6.2 所有路最终汇到同一处

```
                 字节住在哪
   ┌────────────┬────────────────┬──────────────────┐
   │  本地盘     │   网络文件系统   │    对象存储       │
   │  (块设备)   │  (NFS/CephFS)  │      (S3)        │
   └─────┬──────┴───────┬────────┴────────┬─────────┘
         │              │                 │
         │ losetup      │                 │  内核没有 S3 客户端
         │ mkfs         │  mount 直接      │  必须有人翻译：
         │ mount        │  说网络协议      │   ① FUSE（s3fs/goofys/JuiceFS）
         ▼              ▼                 │   ② 先拉到本地盘再挂  ← 缓存/物化模式
   ┌──────────────────────────────┐       │   ③ 不用 CSI，应用走 SDK
   │        节点内核               │◄──────┘
   │  （所有路径最终都经过这里）     │
   └──────────────┬───────────────┘
                  │ bind mount
                  ▼
      /var/lib/kubelet/pods/<uid>/volumes/...
                  │
                  ▼
            容器里的 /data
```

### 6.3 分类表

| 后端 | 谁提供「文件系统」语义 | 需要 attach? | `NodeStageVolume` 干什么 | 多节点共享 |
|---|---|---|---|---|
| 块存储（云盘） | **内核**（ext4/xfs 驱动） | 需要 | `mkfs` + `mount` | ❌ |
| NFS / CephFS | **内核**（自带网络客户端） | 不需要 | 只有 `mount`（**无 mkfs**） | ✅ |
| 对象存储 → FUSE | **用户态进程** | 不需要 | 起 FUSE 进程并挂载 | 视实现 |
| 对象存储 → 先落地 | 内核对**本地盘** | 不需要 | **拉数据**到本地，不碰网络 | ❌（每机一份） |
| 对象存储 → 不用 CSI | — | — | **不存在挂载**，应用走 SDK | ✅ |

### 6.4 CephFS 与 S3 的对照

**CephFS** 是文件存储，Linux 内核里本来就有 ceph 客户端：

```bash
mount -t ceph <mon>:/ /mnt/xxx -o name=...,secret=...
```

由此推出三件事，全都与块存储相反：**不需要 attach**（无「一块设备只能归一台节点」的问题）、**不需要 mkfs**（文件系统在服务端已存在）、**支持 RWX**。

**S3** 内核里没有客户端，无法 `mount -t s3`，必须有人翻译。三种做法：

1. **FUSE** —— `NodePublishVolume` 里起用户态进程，把 HTTP 语义伪装成 POSIX。挂载是「活的」：读一个文件 = 一次 HTTP 请求，**延迟与拉取开销全在关键路径上**。
2. **先落地再挂** —— `NodeStageVolume` 把对象拉到节点本地盘，之后的 mount 退化成对本地目录的 bind mount。**字节在挂载之前就已就位。**
3. **压根不用 CSI** —— 应用自己拿 `s3://...` URI 去拉。

### 6.5 「多机共享」与「本地读」不可兼得

```
   CephFS（共享文件存储）              节点本地缓存（物化模式）
   ─────────────────────────           ──────────────────────────
   一份字节，多机共享                   每台机器各一份
   读走网络                             读走本地盘
   挂载是「活的」（每次读都可能发包）      挂载前字节已就位，挂载只是 bind
   Stage 只做 mount                     Stage 要拉数据 + 判完整性
```

这是一条线上的两个端点，不是两个可以叠加的优点。

---

## 8. 规范强制的部分：幂等与错误语义

### 7.1 幂等不是优化，是规范要求

kubelet 退避重试、sidecar 反复 reconcile，**任何方法都可能被用同样的参数调第二次**。所以：

- `CreateVolume` 同名重试必须返回**同一个卷**，不是建第二个；
- `NodeStageVolume` 重试不能二次 `mkfs`；
- `ControllerPublishVolume` 重试要返回同样的结果。

**实测教训**：`CreateVolume` 的兼容性判据是「已有卷是否落在请求的 `[required_bytes, limit_bytes]` 区间内」——**已有卷更大是兼容的**，应返回该卷；请求比已有卷更大才是 `AlreadyExists`。

### 7.2 「请求不合法」≠「我不支持」

这是两个必须分开的分支：

| 情况 | 语义 | 应答 |
|---|---|---|
| 必填字段缺失（如 `volume_capabilities` 为空） | **请求不合法** | 返回 `InvalidArgument` **错误** |
| 请求合法但本 driver 不支持（如 block 卷） | **否定回答** | 返回**空 response**，`error = nil` |

**实测**：`ValidateVolumeCapabilities` 曾经把两者一起吞成「否定回答」，导致空 `volume_capabilities` 静默返回 `nil` 错误，csi-sanity 报 `Expected an error to have occurred. Got: <nil>`。

### 7.3 能力声明是硬依赖

- `ControllerGetCapabilities` 缺失 ⇒ **`external-provisioner` 启动即 fatal 退出**，表现为 PVC 永远 `Pending`。
- `PUBLISH_UNPUBLISH_VOLUME` 未声明 ⇒ attacher 走空转路径，`ControllerPublishVolume` 永不被调。
- 声明了 `PUBLISH_UNPUBLISH_VOLUME` **不等于**集群会发起 attach —— 那是 `CSIDriver.spec.attachRequired` 的事（见第 10 节）。

---

## 9. 排错地图

**顺序：对象 → 事件 → 日志。** 日志是过程，对象是结果。

| 症状 | 先看什么 |
|---|---|
| PVC 一直 `Pending` | PV 有没有生成 / provisioner 日志 |
| Pod 卡 `ContainerCreating` | Pod events → **有没有 `VolumeAttachment`** |
| `FailedAttachVolume` | `VolumeAttachment` + attacher 日志 |
| `FailedMount` | kubelet 日志 + `NodeStage`/`NodePublish` |
| 改完代码毫无效果 | `apply` 是不是 no-op？DaemonSet 重启了吗？kubelet 是不是在走缓存连接？ |
| 跨节点读到不同数据 | `attachRequired` 现在是 true 还是 false |
| 宿主机看不到 staging 挂载 | `mountPropagation` 是不是 `Bidirectional` |

**选判据的教训**：判据要选**结果**，不是**过程**。这一路上有两次被「名字听起来对」的日志带偏：

- `grep -i 'ControllerPublish'` 会命中启动时那句 `CSI driver supports ControllerPublishUnpublish, using real CSI handler`——那只是能力探测，不代表发生过 attach。
- `grep -i 'AttachVolume'` 在 attacher 日志里**永远不会有输出**：`SuccessfulAttachVolume` 是 attachdetach-controller 发的**事件**（在 `kubectl describe pod` 里），不是容器日志行。

**唯一靠得住的证据是对象**：`kubectl get volumeattachments` 里的 `attached: true`。

---

## 10. 坑位清单（实测）

每个坑背后都有一条不可绕过的原理：

| 坑 | 原理 |
|---|---|
| `NODE_NAME` 必须来自 downward API | node plugin 的身份是**节点**，不是 Pod |
| controller 不挂 `plugins-dir` → staging 挂载宿主机看不见 | `NodeStageVolume` 的挂载发生在 driver 容器的 mount namespace 里 |
| 缺 `Bidirectional` → 挂载传不出去 | 同上，需要显式允许反向传播 |
| `NODE_NAME` 缺失 → 进程直接退出 | 被动 server 没有「稍后补上」的机会，启动即契约 |
| `ControllerGetCapabilities` 缺失 → provisioner fatal | sidecar 启动时探测能力，是硬依赖 |
| 改代码后毫无效果 | 被动方 + kubelet 缓存连接 + `apply` 是 no-op：三个「没人通知你」叠加 |
| `CSIDriver` 缺失 → Pod 卡 `FailedAttachVolume` | 默认值是 `attachRequired: true`——**缺省即声明「我要 attach」** |
| 改了 `attachRequired` 没反应 | 声明层（给 controller-manager）≠ 能力层（给 sidecar） |
| `spec.attachRequired` 改不动 | 不可变字段；改声明的唯一方式是**删掉对象重建** |
| `kubectl apply` 后打 `unchanged` | 三方合并算出空 patch——文件根本没变（不是「默默改了」） |
| RWO 管不住跨节点 | 强制者在 attach 层；没有 attach 就没有强制者 |
| 删/建 Pod 后日志出现一串 `not found` | `VolumeAttachment` 名字是确定性哈希，删建会产生同名 VA 的竞态 |
| `NodeStageVolumeRequest` 没有 `readonly` 字段 | 规范的不对称；只读性只能从 `volume_capability.access_mode` 推 |
| `CreateVolume` 重试不等于建两个卷 | 幂等是规范要求，不是优化 |
| `ValidateVolumeCapabilities` 空列表要报错 | 「请求不合法」与「不支持」是两件事 |
| `SuccessfulAttachVolume` grep 不到 | 它是事件，不是日志行 |

---

## 11. 从原理推设计

拿到一个新后端，填完第 1 节的表，driver 形态就出来了：

```
   ① 字节在哪里？        → 在别处 / 在本机   → 要不要 attach、要不要拓扑
   ② 内核认识这个协议吗？ → 认识 / 不认识     → Stage 要不要 mkfs、要不要拉数据
   ③ 谁触发搬移？         → 决定 Publish 能否阻塞、能不能同步完成
   ④ 数据归谁？           → DeleteVolume 删还是留
   ⑤ 要不要预热？         → 新增对象，还是复用现有触发源
```

以「节点本地模型缓存」为例：

| 问题 | 答案 | 推出的结论 |
|---|---|---|
| 字节在哪里？ | 对象存储（别处） | 需要一次搬移 |
| 搬移产物落在哪？ | 节点本地盘 | 落好之后**不需要 attach**、**不需要拓扑**——任何节点都能自己物化 |
| 谁触发搬移？ | 挂载本身 | **`NodePublishVolume` 绝不能阻塞**——未命中则入队 + 返回可重试错误 |
| 数据归谁？ | 节点级共享 | `DeleteVolume` **不删**节点数据；空间由节点侧 LRU 收回 |
| 要不要预热？ | 要，但不参与调度 | 新增一个 cluster-scoped 对象承载预热范围与每节点状态 |

**「刻意不使用拓扑、卷可跨节点移动」不是设计偏好，而是「字节由节点自己物化」这一条的必然后果。**

同时这也说明预热**不能**变成正确性要求：`HostPath` 那种「可调度节点必须全部预铺」是硬约束，漏一台服务就起不来；而按需物化下，漏铺只是慢一次。少掉的是一整类运维事故。

---

## 附：与 demo 实现的对应

| 概念 | 本仓库的位置 |
|---|---|
| Identity service | `internal/driver/identity.go` |
| Controller service | `internal/driver/controller.go` |
| Node service + 挂载两层 | `internal/driver/node.go`、`internal/mountutil/` |
| 后端抽象 | `internal/backend/backend.go` |
| 目录后端（Task 2） | `internal/backend/localdir.go` |
| 块设备后端（Task 3） | `internal/backend/loopdev.go` |
| 三个 service 的注册与 `--mode` | `cmd/csi-demo/main.go` |
| Controller 侧部署与 sidecar | `deploy/controller.yaml` |
| Node 侧部署与 registrar | `deploy/node.yaml` |
| `CSIDriver` 对象 | `deploy/csidriver.yaml` |
| 一致性测试 | `hack/run-sanity.sh`、`make sanity` |
