package schema

import (
	"github.com/lisama569/versioned-sdk-codegen-go/internal/model"
)

// builder 在解析期间收集实体与枚举，同时处理跨条目命名冲突。
type builder struct {
	version   string
	entities  []model.Entity
	enums     []model.Enum
	nameOwner map[string]string // 名称 -> 类别 "entity"/"enum"
	namePath  map[string]string // 名称 -> 首次声明位置
	collected []Issue
}

func newBuilder() *builder {
	return &builder{
		nameOwner: map[string]string{},
		namePath:  map[string]string{},
	}
}

// declareEnum 预占枚举名；与已有实体/枚举冲突时返回 false。
func (b *builder) declareEnum(name, path string) bool {
	if owner, exists := b.nameOwner[name]; exists {
		b.collected = append(b.collected, issue(KindDuplicateName, path,
			"名称 "+name+" 已被"+ownerLabel(owner)+"占用（首次声明于 "+b.namePath[name]+"）"))
		return false
	}
	b.nameOwner[name] = "enum"
	b.namePath[name] = path
	return true
}

// declareEntity 预占实体名；与已有实体/枚举冲突时返回 false。
func (b *builder) declareEntity(name, path string) bool {
	if owner, exists := b.nameOwner[name]; exists {
		b.collected = append(b.collected, issue(KindDuplicateName, path,
			"名称 "+name+" 已被"+ownerLabel(owner)+"占用（首次声明于 "+b.namePath[name]+"）"))
		return false
	}
	b.nameOwner[name] = "entity"
	b.namePath[name] = path
	return true
}

func (b *builder) addEnum(name string, values []string) {
	b.enums = append(b.enums, model.Enum{Name: name, Values: values})
}

func (b *builder) addEntity(name string, fields []model.Field) {
	b.entities = append(b.entities, model.Entity{Name: name, Fields: fields})
}

func (b *builder) hasEnum(name string) bool {
	return b.nameOwner[name] == "enum"
}

func (b *builder) issues() []Issue { return b.collected }

func (b *builder) build() *model.Schema {
	return &model.Schema{Version: b.version, Entities: b.entities, Enums: b.enums}
}

func ownerLabel(owner string) string {
	if owner == "enum" {
		return "枚举"
	}
	return "实体"
}

// OpenAPI 组件分类支持：组件名先登记为未知，第二轮解析时再归类。
const (
	compUnknown = "unknown"
	compEnum    = "enum"
	compEntity  = "entity"
)

// declareComponentName 登记一个 OpenAPI 组件名（组件名天然唯一，
// 因此同名覆盖会在 JSON 解码阶段发生；此处不处理）。
func (b *builder) declareComponentName(name string) {
	if _, exists := b.nameOwner[name]; !exists {
		b.nameOwner[name] = compUnknown
		b.namePath[name] = "components.schemas." + name
	}
}

func (b *builder) classifyEnum(name string, values []string) {
	b.nameOwner[name] = compEnum
	b.addEnum(name, values)
}

func (b *builder) classifyEntity(name string, fields []model.Field) {
	b.nameOwner[name] = compEntity
	b.addEntity(name, fields)
}

// kindOf 返回已登记名称的组件类别；未登记返回空串。
func (b *builder) kindOf(name string) string {
	return b.nameOwner[name]
}
