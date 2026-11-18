package handler

import (
	"errors"
	"net/http"

	"github.com/Usir233/GENYEE/internal/application/service"
	apperrors "github.com/Usir233/GENYEE/internal/errors"
	"github.com/Usir233/GENYEE/internal/types"
	"github.com/gin-gonic/gin"
)

// GetModelCatalog returns the catalog layers, overlay and version history.
// @Summary      Get the system model catalog
// @Description  System administrator JWT only; API keys are not accepted. Returns built-in, deployment and effective
// @Description  catalogs, the current overlay, version/baseline tokens, and the latest 20 published revisions.
// @Tags         系统管理
// @Produce      json
// @Success      200  {object}  service.CatalogState
// @Failure      401  {object}  apperrors.AppError
// @Failure      403  {object}  apperrors.AppError
// @Failure      503  {object}  apperrors.AppError
// @Security     Bearer
// @Router       /system/admin/model-catalog [get]
func (h *SystemHandler) GetModelCatalog(c *gin.Context) {
	state, err := h.modelCatalogSvc.State(c.Request.Context())
	if err != nil {
		writeAppError(c, apperrors.NewServiceUnavailableError("Model catalog is unavailable"))
		return
	}
	c.JSON(http.StatusOK, state)
}

// PreviewModelCatalog validates an overlay and returns the candidate catalog.
// @Summary      Preview a model catalog overlay
// @Description  System administrator JWT only; API keys are not accepted. Validates the overlay without publishing
// @Description  it. Supply the current version and baseline; stale values return 409. The response contains the
// @Description  candidate effective catalog and normalized overlay; history, built-in and deployment layers are empty.
// @Tags         系统管理
// @Accept       json
// @Produce      json
// @Param        request  body  service.CatalogUpdate  true  "Version, baseline and overlay object (max 1 MiB)"
// @Success      200  {object}  service.CatalogState
// @Failure      400  {object}  apperrors.AppError
// @Failure      401  {object}  apperrors.AppError
// @Failure      403  {object}  apperrors.AppError
// @Failure      409  {object}  apperrors.AppError
// @Failure      500  {object}  apperrors.AppError
// @Security     Bearer
// @Router       /system/admin/model-catalog/preview [post]
func (h *SystemHandler) PreviewModelCatalog(c *gin.Context) { h.changeModelCatalog(c, false) }

// PublishModelCatalog validates and persists an overlay as a new revision.
// @Summary      Publish a model catalog overlay
// @Description  System administrator JWT only; API keys are not accepted. Validates and saves the overlay with
// @Description  optimistic concurrency on version and baseline, refreshes this replica, and notifies other replicas.
// @Tags         系统管理
// @Accept       json
// @Produce      json
// @Param        request  body  service.CatalogUpdate  true  "Version, baseline and overlay object (max 1 MiB)"
// @Success      200  {object}  service.CatalogState
// @Failure      400  {object}  apperrors.AppError
// @Failure      401  {object}  apperrors.AppError
// @Failure      403  {object}  apperrors.AppError
// @Failure      409  {object}  apperrors.AppError
// @Failure      500  {object}  apperrors.AppError
// @Security     Bearer
// @Router       /system/admin/model-catalog [put]
func (h *SystemHandler) PublishModelCatalog(c *gin.Context) { h.changeModelCatalog(c, true) }

func (h *SystemHandler) changeModelCatalog(c *gin.Context, publish bool) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, service.MaxCatalogOverlayBytes+4096)
	var req service.CatalogUpdate
	if err := c.ShouldBindJSON(&req); err != nil {
		writeAppError(c, apperrors.NewBadRequestError("Invalid model catalog request (maximum 1 MiB)"))
		return
	}
	var state *service.CatalogState
	var err error
	if publish {
		state, err = h.modelCatalogSvc.Publish(c.Request.Context(), req)
	} else {
		state, err = h.modelCatalogSvc.Preview(c.Request.Context(), req)
	}
	if err != nil {
		switch {
		case errors.Is(err, types.ErrCatalogVersionConflict):
			writeAppError(c, apperrors.NewConflictError(err.Error()))
		case errors.Is(err, service.ErrInvalidCatalog):
			writeAppError(c, apperrors.NewBadRequestError(err.Error()))
		default:
			writeAppError(c, apperrors.NewInternalServerError("Failed to update model catalog"))
		}
		return
	}
	c.JSON(http.StatusOK, state)
}
