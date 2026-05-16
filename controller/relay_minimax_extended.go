package controller

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel/minimax"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
)

// RelayMiniMaxExtended 处理 MiniMax 私有扩展端点（files/upload / voice_clone / music_generation）。
// 这些端点是 MiniMax 私有协议，不是 OpenAI 兼容，但因为请求体里有 model 字段，
// 可以复用 Distribute 中间件做渠道分发，再由本 handler 自行 forward 上游 + 计费。
// 计费模型：按次 ModelPrice × 1（QuotaPerCall）。
func RelayMiniMaxExtended(c *gin.Context) {
	relayMode := c.GetInt("relay_mode")
	if relayMode == 0 {
		relayMode = relayconstant.Path2RelayMode(c.Request.URL.Path)
	}

	// 构造最小化的 RelayInfo（无 dto.Request，因为这些路径不是标准 OpenAI 格式）
	relayInfo := relaycommon.GenRelayInfoOpenAI(c, nil)
	relayInfo.RelayMode = relayMode
	relayInfo.InitChannelMeta(c)

	// 计费：按次 ModelPrice × 1。files/upload 走 0 价（ModelPrice 配 0 即可）。
	priceData, priceErr := helper.ModelPriceHelperPerCall(c, relayInfo)
	if priceErr != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": types.NewError(priceErr, types.ErrorCodeModelPriceError).ToOpenAIError(),
		})
		return
	}
	relayInfo.PriceData = priceData

	var apiErr *types.NewAPIError
	if !priceData.FreeModel && priceData.Quota > 0 {
		apiErr = service.PreConsumeBilling(c, priceData.Quota, relayInfo)
		if apiErr != nil {
			c.JSON(apiErr.StatusCode, gin.H{"error": apiErr.ToOpenAIError()})
			return
		}
	}

	defer func() {
		if apiErr != nil && relayInfo.Billing != nil {
			relayInfo.Billing.Refund(c)
		}
	}()

	switch relayMode {
	case relayconstant.RelayModeMiniMaxFilesUpload:
		_, apiErr = minimax.RelayMiniMaxFilesUpload(c, relayInfo)
	case relayconstant.RelayModeMiniMaxVoiceClone:
		_, apiErr = minimax.RelayMiniMaxVoiceClone(c, relayInfo)
	case relayconstant.RelayModeMiniMaxMusic:
		_, apiErr = minimax.RelayMiniMaxMusic(c, relayInfo)
	default:
		apiErr = types.NewErrorWithStatusCode(
			errors.New("unsupported minimax extended relay mode"),
			types.ErrorCodeInvalidRequest,
			http.StatusBadRequest,
		)
	}

	if apiErr != nil {
		// 状态码 < 500 时返回上游错误；否则透传错误（响应体可能已经写过）
		if !c.Writer.Written() {
			c.JSON(apiErr.StatusCode, gin.H{"error": apiErr.ToOpenAIError()})
		}
		return
	}

	// 结算：按次扣 ModelPrice × 1。即使免费模型（Quota=0）也走一遍流程做日志
	if relayInfo.Billing != nil {
		if settleErr := service.SettleBilling(c, relayInfo, priceData.Quota); settleErr != nil {
			logger.LogError(c, fmt.Sprintf("settle minimax extended billing error: %s", settleErr.Error()))
		}
	}

	// 记录消费日志（不再扣费，仅记录）
	logMiniMaxConsumption(c, relayInfo)
}

// logMiniMaxConsumption 记录 MiniMax 扩展端点的消费日志。
// 模仿 service.LogTaskConsumption，但简化（这些是按次计费的非任务请求）。
func logMiniMaxConsumption(c *gin.Context, info *relaycommon.RelayInfo) {
	tokenName := c.GetString("token_name")
	logContent := fmt.Sprintf("MiniMax %s 按次计费", c.Request.URL.Path)

	other := map[string]interface{}{
		"request_path": c.Request.URL.Path,
		"model_price":  info.PriceData.ModelPrice,
		"group_ratio":  info.PriceData.GroupRatioInfo.GroupRatio,
	}
	if info.PriceData.GroupRatioInfo.HasSpecialRatio {
		other["user_group_ratio"] = info.PriceData.GroupRatioInfo.GroupSpecialRatio
	}

	model.RecordConsumeLog(c, info.UserId, model.RecordConsumeLogParams{
		ChannelId: info.ChannelId,
		ModelName: info.OriginModelName,
		TokenName: tokenName,
		Quota:     info.PriceData.Quota,
		Content:   logContent,
		TokenId:   info.TokenId,
		Group:     info.UsingGroup,
		Other:     other,
	})
	if info.PriceData.Quota > 0 {
		model.UpdateUserUsedQuotaAndRequestCount(info.UserId, info.PriceData.Quota)
		model.UpdateChannelUsedQuota(info.ChannelId, info.PriceData.Quota)
	}
}
