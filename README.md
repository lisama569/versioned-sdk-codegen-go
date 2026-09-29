# versioned-sdk-codegen-go

由 schema 定义驱动的版本化 SDK 生成工具链。输入多份、不同写法的 schema，
归一化到统一内部模型后：

1. **归一化（normalize）**：把 simple JSON 与 OpenAPI 风格两种方言解析成统一模型；
   任何字段缺失、类型非法、命名冲突的条目都会**按原因归类上报，绝不静默跳过**。
2. **兼容性判定（compat）**：对新旧两版 schema 做有序比较，输出确定性排序的变更清单，
   破坏性变更显式标记，兼容变更放行；同一对 schema 反复判定结论完全一致。
3. **代码生成（generate）**：渲染出可独立编译的版本化 Go SDK；同一份 schema 的并发生成
   按 key 串行化，产物与串行执行逐字节一致；先在临时目录渲染、gofmt、go build 校验，
   再原子替换，失败不留半截产物。

## 目录结构

```
internal/model/     统一内部模型（Schema/Entity/Field/Type/Enum）
internal/normalize/ 两种方言 -> 统一模型；按 IssueCode 分类的错误收集
internal/compat/    新旧 schema 兼容性判定，确定性变更报告
internal/gen/       确定性渲染 + 暂存编译 + 原子替换 + 按 key 串行化
cmd/sdkgen/         CLI：normalize / compat / generate
```

## Schema 处理规则

### 支持的方言

**simple 方言**（检测特征：顶层存在 `entities` 或 `enums`）：

```json
{
  "version": "v2",
  "entities": [
    {
      "name": "User",
      "fields": [
        {"name": "id", "type": "string"},
        {"name": "age", "type": "integer", "optional": true, "note": "可选"},
        {"name": "tags", "type": "string[]"},
        {"name": "tags2", "type": {"kind": "array", "items": "string"}},
        {"name": "role", "type": {"kind": "enum", "name": "Role"}},
        {"name": "kind", "type": {"kind": "enum", "values": ["a", "b"]}},
        {"name": "addr", "type": {"kind": "ref", "name": "Address"}}
      ]
    }
  ],
  "enums": [{"name": "Role", "values": ["admin", "member"]}]
}
```

**OpenAPI 3 方言**（检测特征：顶层存在 `openapi` 或 `components`）：
取 `info.version` 与 `components.schemas` 子集——带 `enum` 的组件是枚举，
`type: object`（或带 `properties`）的组件是实体；属性的必填性由 `required` 决定，
`$ref: "#/components/schemas/X"` 指向枚举/实体。

两种方言解析到**同一个内部模型**（原始类型别名 `integer/long→int`、
`boolean→bool`、`number/double→float`、`str→string` 会被折叠），
因此跨方言可以直接比较。

### 归一化口径

- 顶层必须是 JSON 对象；`version`（simple）/ `info.version`（openapi）必填且非空。
- 实体、枚举、字段都必须有非空名字；名字还必须是合法 Go 标识符
  （实体/枚举/字段名、枚举值都要满足，因为它们会成为生成代码里的类型、字段和常量名）。
- 字段必须有类型；类型必须是受支持的构造：
  `string/int/bool/float` 及其别名、`array`（带 `items`）、
  命名 `enum`（必须引用已存在的枚举）、内联 `enum`（非空 values）、
  `ref`（必须引用已存在的实体）。
- 实体与枚举共享同一个命名空间：重名、跨类型撞名、重复字段名、重复枚举值
  都按命名冲突上报。
- OpenAPI 里非 object 又非 enum 的组件、无法解析的 `$ref` 同样上报，不跳过。
- 一份文档里的所有问题会被**全部收集**（不因第一个错误中断），
  按路径自然序（`fields[2]` 排在 `fields[10]` 前）排序、去重后返回；
  只要存在任何 issue，归一化就返回错误且不返回模型，避免畸形模型流入后续阶段。

### 错误分类（IssueCode）

| Code                | 含义                                         |
|---------------------|----------------------------------------------|
| `invalid_syntax`    | JSON 非法、根不是对象、结构形状错误          |
| `unsupported_format`| 方言无法识别，或出现方言不支持的组件形状     |
| `missing_version`   | 缺少 schema 版本                             |
| `missing_name`      | 实体/枚举/字段缺少非空名称                   |
| `invalid_name`      | 名称存在但不是合法 Go 标识符                 |
| `missing_type`      | 字段/数组元素缺少类型                        |
| `invalid_type`      | 类型构造非法、未知原始类型、坏 `$ref` 语法等 |
| `unknown_ref`       | 类型像引用（大写开头或 `$ref`）但目标不存在  |
| `name_conflict`     | 重名、实体/枚举撞名、字段或枚举值重复        |

Go 侧通过 `normalize.AsIssues(err)` / `issues.Has(code)` 按原因归类。

## 兼容性判定口径

判定视角：**已经在消费 old 版本的老客户端是否会被 new 版本破坏**。

| 变更                                      | Kind                          | 结论       |
|-------------------------------------------|-------------------------------|------------|
| 删除实体 / 删除枚举                       | `entity_removed`/`enum_removed` | breaking   |
| 删除字段                                  | `field_removed`               | breaking   |
| 字段类型变化（含原始类型互转、数组化、命名/内联枚举收紧） | `field_type_changed` | breaking |
| 可选字段变必填                            | `field_optionality_tightened` | breaking   |
| 新增**必填**字段（老客户端不提供）        | `field_added_required`        | breaking   |
| 删除枚举允许值（含内联枚举）              | `enum_value_removed`          | breaking   |
| 新增实体 / 新增枚举                       | `entity_added`/`enum_added`     | compatible |
| 新增**可选**字段                          | `field_added_optional`        | compatible |
| 必填字段变可选                            | `field_optionality_relaxed`   | compatible |
| 枚举增加允许值                            | `enum_value_added`            | compatible |
| 两版完全相同                              | （无变更）                    | compatible |

- 类型在归一化后比较，故 `integer`→`int` 这类纯写法差异不算变更。
- 变更清单按 `path`（实体/字段/枚举值位置）自然序排序，比较过程不依赖
  map 遍历顺序；因此**同一对 schema 任意次数、任意顺序调用结论与排序完全一致**
  （有对应可重复性测试）。

## 代码生成口径

- 输出是一个可独立编译的 Go module（自带 `go.mod`，无外部依赖），
  包名 `sdk` + 版本号净化结果（如 `v1`、`v1_2`）。
- 文件固定：`go.mod`、`doc.go`（含版本常量）、`client.go`（每个实体的
  Create/Get 方法）、每个实体一个 `types_<name>.go`、每个枚举一个 `enum_<name>.go`。
- JSON tag 保留 schema 原名；Go 字段/常量名由原名首字母大写导出。
- 渲染是纯函数：对相同模型产生相同的 `map[path][]byte`，写入前按路径排序，
  因此产物与声明顺序、map 遍历顺序、并发时序均无关。
- `Service.Generate(key, schema, opts)` 以 key 维度加锁：同一份 schema 的
  并发生成会串行执行，产物与串行调用逐字节一致；不同 key 互不阻塞。
- 原子性：先写入目标目录旁的 `.sdkgen-*` 临时目录 → `gofmt -w` → `go build ./...`
  校验 → 旧目录改名备份 → 临时目录改名为目标 → 删除备份。
  任一步失败都会清理临时目录；最终改名失败会把备份还原，**目标路径不会留下半成品**。

## CLI 用法

```bash
# 归一化查看（畸形输入返回非零退出码，stdout 输出分类后的 issues JSON）
go run ./cmd/sdkgen normalize [--format auto|simple|openapi] schema.json

# 兼容性判定（old 在前，new 在后；两边可以是不同方言）
go run ./cmd/sdkgen compat [--old-format auto] [--new-format auto] old.json new.json

# 生成 SDK（--key 是同一份 schema 并发去重的标识）
go run ./cmd/sdkgen generate --key users-v2 --out ./sdk/v2 [--format auto] schema.json
```

## 本地验证

```bash
# 全量测试（含畸形 schema、破坏性变更、并发与原子性边界）
go test ./...

# 查看输入与判定依据：所有测试都在日志中打印原始输入和逐条结论
go test -v ./internal/normalize/ ./internal/compat/

# 并发安全验证
go test -race ./internal/gen/

# 静态检查
go vet ./...

# 手动端到端
go run ./cmd/sdkgen compat path/to/v1.json path/to/v2.json
go run ./cmd/sdkgen generate --key demo --out /tmp/demo-sdk path/to/v1.json
(cd /tmp/demo-sdk && go build ./...)
```

测试覆盖要点：

- 两种方言归一到同一模型；18+ 类畸形输入各自命中正确的 IssueCode；
  多问题收集、排序、不丢弃。
- 全部 breaking / compatible 变更分类、内联枚举收紧、跨方言端到端判定、
  同一对 schema 反复判定结果一致。
- 渲染确定性（乱序输入产物一致）、8 路并发同 key 产物与串行一致、
  同目录并发写、渲染失败/暂存编译失败/最终改名失败三种失败路径均不留半成品
  且旧产物可回滚。
