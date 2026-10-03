package usecase

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"family-finances/internal/domain"
	"family-finances/internal/port"
)

// MaxBatchTxIDs 一次批量操作的上限。装修一次上百笔，给足余量但不放任无界请求。
// 两个适配器（SSR handler 与 /api/v1）共用这一个常量。
const MaxBatchTxIDs = 1000

// maxMemberRunes 成员标注的字数上限
const maxMemberRunes = 20

// TxWriter 单条/批量改流水，由 sqlite.TransactionRepo 满足。
type TxWriter interface {
	Update(ctx context.Context, id string, patch port.TransactionUpdate) error
	SetSpecialForIDs(ctx context.Context, ids []string, specialID string) (int, error)
}

// SpecialEnsurer 校验专项存在，由 *SpecialView 满足。
// 不存在时返回包装了 port.ErrNotFound 的错误，其它错误是故障。
type SpecialEnsurer interface {
	Ensure(ctx context.Context, id string) error
}

// LeafCategoryLister 校验 category_id 是二级科目用，只要 ListAll
type LeafCategoryLister interface {
	ListAll(ctx context.Context) ([]domain.Category, error)
}

// TxPatch 单条流水的更新请求：每个字段都是可选指针，nil = 不动。
// 空串的含义：category_id="" 清空分类，special_id="" 归回日常，note/member="" 清空。
type TxPatch struct {
	CategoryID *string
	Note       *string
	Status     *string
	Account    *string
	Member     *string
	SpecialID  *string
}

// UpdateTransaction 流水就地编辑用例：全部入参校验与 patch 装配都在这里，
// SSR handler 与 /api/v1 只做 HTTP 解析与响应格式。
// 校验失败返回 ErrInvalidInput（Error() 是面向用户的中文）；流水不存在返回包装了 port.ErrNotFound 的错误；
// 其余是内部故障。
type UpdateTransaction struct {
	tx       TxWriter
	cats     LeafCategoryLister
	specials SpecialEnsurer // nil = 专项功能未启用

	// 「按筛选批量改」所需；未注入时 AssignSpecialByFilter 返回内部错误
	filter FilterResolver
	bulk   port.TransactionBulkRepo
}

// FilterResolver 把原始列表请求归一成筛选条件，由 *TxQuery 满足。
type FilterResolver interface {
	ResolveFilter(ctx context.Context, req TxQueryRequest) (port.TransactionQuery, error)
}

// WithFilter 注入「按筛选批量改」的依赖。resolver 必须就是列表用的那个 TxQuery，
// 这样归一规则只有一份。
func (uc *UpdateTransaction) WithFilter(r FilterResolver, bulk port.TransactionBulkRepo) *UpdateTransaction {
	uc.filter, uc.bulk = r, bulk
	return uc
}

// NewUpdateTransaction specials 可为 nil（专项功能未启用：传非空 special_id 会被拒）。
func NewUpdateTransaction(tx TxWriter, cats LeafCategoryLister, specials SpecialEnsurer) *UpdateTransaction {
	return &UpdateTransaction{tx: tx, cats: cats, specials: specials}
}

// Update 校验并应用单条更新。
func (uc *UpdateTransaction) Update(ctx context.Context, id string, p TxPatch) error {
	if id == "" {
		return newUserError(ErrInvalidInput, "缺少流水 id")
	}
	patch := port.TransactionUpdate{}
	if p.CategoryID != nil {
		v := *p.CategoryID
		if v != "" {
			if err := uc.ensureLeafCategory(ctx, v); err != nil {
				return err
			}
		}
		patch.CategoryID = &v
		// 指定了分类就把状态自动转 confirmed；若同一请求显式给了 status，下面会覆盖它
		if v != "" {
			st := domain.TxStatusConfirmed
			patch.Status = &st
		}
	}
	if p.Note != nil {
		v := *p.Note
		patch.Note = &v
	}
	if p.Status != nil {
		s := domain.TxStatus(*p.Status)
		switch s {
		case domain.TxStatusPendingReview, domain.TxStatusConfirmed, domain.TxStatusExcluded:
		default:
			return newUserError(ErrInvalidInput, "状态不合法")
		}
		patch.Status = &s
	}
	if p.Account != nil {
		ac := domain.Account(*p.Account)
		if !ac.IsStorageAccount() {
			return newUserError(ErrInvalidInput, "账户不合法")
		}
		patch.Account = &ac
	}
	if p.Member != nil {
		m := strings.TrimSpace(*p.Member)
		if len([]rune(m)) > maxMemberRunes {
			return newUserError(ErrInvalidInput, fmt.Sprintf("成员标注过长（限 %d 字）", maxMemberRunes))
		}
		patch.Member = &m
	}
	if p.SpecialID != nil {
		v := strings.TrimSpace(*p.SpecialID)
		if err := uc.ensureSpecial(ctx, v); err != nil {
			return err
		}
		patch.SpecialID = &v
	}
	return uc.tx.Update(ctx, id, patch)
}

// AssignSpecial 批量把流水归入专项（specialID 指向空串 = 归回日常），返回实际改动条数。
// specialID 为 nil 视为缺参。空 id 在进 SQL 前滤掉；库里不存在的 id 由 repo 静默跳过
// （客户端列表可能已过期），不算整体失败。
func (uc *UpdateTransaction) AssignSpecial(ctx context.Context, ids []string, specialID *string) (int, error) {
	if len(ids) == 0 {
		return 0, newUserError(ErrInvalidInput, "请选择要归类的流水")
	}
	if len(ids) > MaxBatchTxIDs {
		return 0, newUserError(ErrInvalidInput, fmt.Sprintf("一次最多处理 %d 条流水", MaxBatchTxIDs))
	}
	if specialID == nil {
		return 0, newUserError(ErrInvalidInput, "缺少 special_id")
	}
	sid := strings.TrimSpace(*specialID)
	if err := uc.ensureSpecial(ctx, sid); err != nil {
		return 0, err
	}
	clean := make([]string, 0, len(ids))
	for _, id := range ids {
		if id != "" {
			clean = append(clean, id)
		}
	}
	// 一个事务里一次写完：逐条 Update 会开上千个隐式事务（1000 条 332ms vs 18ms），
	// 而且中途失败时前面已提交的部分回滚不掉，用户会拿到一个说不清改了多少的半成品。
	return uc.tx.SetSpecialForIDs(ctx, clean, sid)
}

// AssignSpecialByFilter 把「与列表接口同一套筛选条件」命中的全部流水（不止当页）归入专项，
// specialID 指向空串 = 归回日常，返回命中条数。排序与分页参数被忽略。
// 专项校验与 AssignSpecial 同一套（不存在 = ErrInvalidInput，DB 故障原样返回）。
func (uc *UpdateTransaction) AssignSpecialByFilter(ctx context.Context, req TxQueryRequest, specialID *string) (int, error) {
	if specialID == nil {
		return 0, newUserError(ErrInvalidInput, "缺少 special_id")
	}
	sid := strings.TrimSpace(*specialID)
	if err := uc.ensureSpecial(ctx, sid); err != nil {
		return 0, err
	}
	if uc.filter == nil || uc.bulk == nil {
		return 0, errors.New("按筛选批量改未装配")
	}
	q, err := uc.filter.ResolveFilter(ctx, req)
	if err != nil {
		return 0, err
	}
	// 一个事务一条 UPDATE，WHERE 与列表查询共用 buildTxWhere
	return uc.bulk.SetSpecialByQuery(ctx, q, sid)
}

// ensureLeafCategory category_id 必须是二级科目。ListAll 出错是内部故障，原样返回。
func (uc *UpdateTransaction) ensureLeafCategory(ctx context.Context, id string) error {
	cats, err := uc.cats.ListAll(ctx)
	if err != nil {
		return err
	}
	for _, c := range cats {
		if c.ID == id && c.Level == 2 {
			return nil
		}
	}
	return newUserError(ErrInvalidInput, "请选择有效的二级分类")
}

// ensureSpecial 空串（清空）直接放行；非空时专项功能必须已启用且专项存在。
// 「不存在」是用户错（ErrInvalidInput），Ensure 的其它错误是 DB 故障，原样返回。
func (uc *UpdateTransaction) ensureSpecial(ctx context.Context, id string) error {
	if id == "" {
		return nil
	}
	if uc.specials == nil {
		return newUserError(ErrInvalidInput, "专项功能未启用")
	}
	if err := uc.specials.Ensure(ctx, id); err != nil {
		if errors.Is(err, port.ErrNotFound) {
			return newUserError(ErrInvalidInput, "专项不存在")
		}
		return err
	}
	return nil
}
