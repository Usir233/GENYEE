package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Usir233/GENYEE/internal/types"
	"github.com/Usir233/GENYEE/internal/types/interfaces"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type resourceRepository struct{ db *gorm.DB }

// NewResourceRepository creates the persistence adapter for resource metadata.
func NewResourceRepository(db *gorm.DB) interfaces.ResourceRepository {
	return &resourceRepository{db: db}
}

func (r *resourceRepository) Create(ctx context.Context, resource *types.StoredResource) error {
	return r.db.WithContext(ctx).Create(resource).Error
}

func (r *resourceRepository) GetByID(ctx context.Context, id string) (*types.StoredResource, error) {
	var resource types.StoredResource
	err := r.db.WithContext(ctx).Where("id = ? AND state = ?", id, types.ResourceStateActive).First(&resource).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &resource, err
}

func (r *resourceRepository) BindStorageBackend(
	ctx context.Context, id, backendID string,
) (*types.StoredResource, error) {
	err := r.db.WithContext(ctx).Model(&types.StoredResource{}).
		Where("id = ? AND state = ? AND (storage_backend_id = '' OR storage_backend_id IS NULL)",
			id, types.ResourceStateActive).
		Update("storage_backend_id", backendID).Error
	if err != nil {
		return nil, err
	}
	return r.GetByID(ctx, id)
}

func (r *resourceRepository) GetByHandle(ctx context.Context, handle string) (*types.StoredResource, error) {
	var resource types.StoredResource
	err := r.db.WithContext(ctx).
		Where("handle = ? AND state = ?", handle, types.ResourceStateActive).
		First(&resource).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &resource, err
}

func (r *resourceRepository) GetByTenantLocation(
	ctx context.Context,
	tenantID uint64,
	locationHash string,
) (*types.StoredResource, error) {
	var resource types.StoredResource
	err := r.db.WithContext(ctx).
		Where(
			"tenant_id = ? AND location_hash = ? AND state = ?",
			tenantID,
			locationHash,
			types.ResourceStateActive,
		).
		First(&resource).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &resource, err
}

// GetActiveByContentHash resolves identical bytes already registered under any
// physical location. The content hash is not unique — duplicates written before
// this lookup existed may share it — so the oldest live row is returned to keep
// the choice stable between calls.
func (r *resourceRepository) GetActiveByContentHash(
	ctx context.Context,
	tenantID uint64,
	contentHash string,
) (*types.StoredResource, error) {
	return r.getActiveByContentHash(ctx, tenantID, contentHash, nil)
}

func (r *resourceRepository) GetActiveByContentHashInStorage(
	ctx context.Context, tenantID uint64, contentHash, backendID string,
) (*types.StoredResource, error) {
	return r.getActiveByContentHash(ctx, tenantID, contentHash, &backendID)
}

func (r *resourceRepository) getActiveByContentHash(
	ctx context.Context, tenantID uint64, contentHash string, backendID *string,
) (*types.StoredResource, error) {
	var resource types.StoredResource
	query := r.db.WithContext(ctx).
		Where(
			"tenant_id = ? AND content_hash = ? AND content_hash <> '' "+
				"AND state = ? AND deleted_at IS NULL",
			tenantID,
			contentHash,
			types.ResourceStateActive,
		).Where("lifecycle = ?", types.ResourceLifecyclePersistent)
	if backendID != nil {
		if *backendID == "" {
			query = query.Where("storage_backend_id = '' OR storage_backend_id IS NULL")
		} else {
			query = query.Where("storage_backend_id = ?", *backendID)
		}
	}
	err := query.Order("created_at ASC, id ASC").
		First(&resource).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &resource, err
}

func (r *resourceRepository) MarkDeleted(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Model(&types.StoredResource{}).Where("id = ?", id).
		Updates(map[string]interface{}{"state": types.ResourceStateDeleted, "deleted_at": time.Now()}).Error
}

func (r *resourceRepository) CreateBinding(ctx context.Context, binding *types.ResourceBinding) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if binding.OwnerType == types.ResourceOwnerResource {
			handle, ok := types.ParseResourcePath(binding.OwnerID)
			if !ok {
				return fmt.Errorf("invalid parent resource reference")
			}
			// Always lock the parent before its derived image. A deleted parent
			// cannot acquire new children after its cleanup has listed them.
			if err := tx.Model(&types.StoredResource{}).Where("handle = ?", handle).
				UpdateColumn("updated_at", gorm.Expr("updated_at")).Error; err != nil {
				return err
			}
			var parent types.StoredResource
			if err := tx.Where("handle = ? AND tenant_id = ? AND state = ?", handle, binding.TenantID, types.ResourceStateActive).
				First(&parent).Error; err != nil {
				return err
			}
		}
		// The write obtains the same resource-row lock as cleanup, including on
		// SQLite, where SELECT FOR UPDATE is not supported. Recheck after locking:
		// the catalog's earlier Resolve may have raced a cleanup transaction.
		if err := tx.Model(&types.StoredResource{}).Where("id = ?", binding.ResourceID).
			UpdateColumn("updated_at", gorm.Expr("updated_at")).Error; err != nil {
			return err
		}
		var resource types.StoredResource
		if err := tx.Where("id = ? AND state = ?", binding.ResourceID, types.ResourceStateActive).
			First(&resource).Error; err != nil {
			return err
		}
		return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(binding).Error
	})
}

func (r *resourceRepository) ClaimUnboundDeletion(ctx context.Context, handle string) (*types.StoredResource, error) {
	var claimed *types.StoredResource
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Unscoped().Model(&types.StoredResource{}).Where("handle = ?", handle).
			UpdateColumn("updated_at", gorm.Expr("updated_at")).Error; err != nil {
			return err
		}
		var resource types.StoredResource
		if err := tx.Unscoped().Where("handle = ?", handle).First(&resource).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		if resource.State != types.ResourceStateDeleted {
			var bindings []types.ResourceBinding
			if err := tx.Where("resource_id = ?", resource.ID).Find(&bindings).Error; err != nil {
				return err
			}
			for _, binding := range bindings {
				// A deleted parent keeps its dependency binding as a durable
				// cleanup work item until the provider deletion succeeds.
				if binding.OwnerType != types.ResourceOwnerResource {
					return nil
				}
				handle, valid := types.ParseResourcePath(binding.OwnerID)
				if !valid {
					return nil
				}
				var parent types.StoredResource
				if err := tx.Unscoped().Where("handle = ?", handle).First(&parent).Error; err != nil {
					return err
				}
				if parent.State != types.ResourceStateDeleted {
					return nil
				}
			}
			if err := tx.Model(&types.StoredResource{}).Where("id = ?", resource.ID).
				Updates(map[string]interface{}{"state": types.ResourceStateDeleted, "deleted_at": time.Now()}).Error; err != nil {
				return err
			}
		}
		claimed = &resource
		return nil
	})
	return claimed, err
}

func (r *resourceRepository) GetResourceForCleanup(ctx context.Context, handle string) (*types.StoredResource, error) {
	var resource types.StoredResource
	if err := r.db.WithContext(ctx).Unscoped().Where("handle = ?", handle).First(&resource).Error; err != nil {
		return nil, err
	}
	return &resource, nil
}

func (r *resourceRepository) ListDeletionDependents(ctx context.Context, parent string) ([]string, error) {
	var handles []string
	err := r.db.WithContext(ctx).Table("resource_bindings AS b").
		Joins("JOIN resources AS r ON r.id = b.resource_id").
		Where("b.owner_type = ? AND b.owner_id = ?", types.ResourceOwnerResource, parent).
		Distinct("r.handle").Pluck("r.handle", &handles).Error
	return handles, err
}

func (r *resourceRepository) ReleaseDeletionDependent(ctx context.Context, handle, parent string) error {
	return r.db.WithContext(ctx).Where("resource_id IN (?) AND owner_type = ? AND owner_id = ?",
		r.db.Unscoped().Model(&types.StoredResource{}).Select("id").Where("handle = ?", handle),
		types.ResourceOwnerResource, parent).Delete(&types.ResourceBinding{}).Error
}

// ReleaseBindingByHandle includes tombstones so a failed physical deletion can
// be retried through the same owner-release API after its first attempt.
func (r *resourceRepository) ReleaseBindingByHandle(ctx context.Context, handle, ownerType, ownerID string) (int64, error) {
	var resource types.StoredResource
	if err := r.db.WithContext(ctx).Unscoped().Where("handle = ?", handle).First(&resource).Error; err != nil {
		return -1, err
	}
	if err := r.DeleteBinding(ctx, resource.ID, ownerType, ownerID); err != nil {
		return -1, err
	}
	return r.CountBindings(ctx, resource.ID)
}

// DeleteBinding removes one owner's claim on a resource. Deleting a claim that
// was never recorded is not an error: callers release optimistically, from a
// content scan that cannot know which references were bound.
func (r *resourceRepository) DeleteBinding(ctx context.Context, resourceID, ownerType, ownerID string) error {
	return r.db.WithContext(ctx).
		Where("resource_id = ? AND owner_type = ? AND owner_id = ?", resourceID, ownerType, ownerID).
		Delete(&types.ResourceBinding{}).Error
}

func (r *resourceRepository) CountBindings(ctx context.Context, resourceID string) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&types.ResourceBinding{}).
		Where("resource_id = ?", resourceID).
		Count(&count).Error
	return count, err
}

func (r *resourceRepository) ListHandlesByOwner(
	ctx context.Context, ownerType string, ownerIDs, relations []string,
) ([]string, error) {
	if ownerType == "" || len(ownerIDs) == 0 || len(relations) == 0 {
		return nil, nil
	}
	var handles []string
	err := r.db.WithContext(ctx).
		Table("resource_bindings AS b").
		Joins("JOIN resources AS r ON r.id = b.resource_id AND r.state = ?", types.ResourceStateActive).
		Where("b.owner_type = ? AND b.owner_id IN ? AND b.relation IN ?", ownerType, ownerIDs, relations).
		Distinct("r.handle").
		Pluck("r.handle", &handles).Error
	return handles, err
}

func (r *resourceRepository) CreateGrant(ctx context.Context, grant *types.ResourceAccessGrant) error {
	return r.db.WithContext(ctx).Create(grant).Error
}

func (r *resourceRepository) GetValidGrant(
	ctx context.Context,
	tokenHash string,
	now time.Time,
) (*types.ResourceAccessGrant, error) {
	var grant types.ResourceAccessGrant
	err := r.db.WithContext(ctx).
		Where("token_hash = ? AND revoked_at IS NULL AND expires_at > ?", tokenHash, now).
		First(&grant).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &grant, err
}

// DeleteExpiredGrants drops grants that are past their expiry. A revoked grant
// is kept until then on purpose: it is the tombstone that stops a token derived
// for the same resource and window from re-creating the row and reviving access.
func (r *resourceRepository) DeleteExpiredGrants(ctx context.Context, before time.Time) error {
	return r.db.WithContext(ctx).
		Where("expires_at <= ?", before).
		Delete(&types.ResourceAccessGrant{}).Error
}
