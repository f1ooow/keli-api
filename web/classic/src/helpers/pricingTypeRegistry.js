/*
Copyright (C) 2025 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/

// 前端单一真相源：模型名 → pricingType → 公式 / 单位 / 倍率表。
// 不改后端 Pricing struct（quota_type 只有 0/1），由前端按 model_name 前缀
// 推导出扩展 pricingType，包括按 Token、按次、按秒、按分钟、按字符和视频专用计费。
// 未匹配模型走 quota_type=0/1 fallback，与改造前 100% 行为一致。

const formatNumber = (n, digits = 4) => {
  if (n === null || n === undefined || Number.isNaN(Number(n))) return '-';
  const num = Number(n);
  if (!Number.isFinite(num)) return '-';
  return Number(num.toFixed(digits)).toString();
};

// 按 newapi 后端 relay/channel/task/ali/adaptor.go aliRatios 复刻的分辨率倍率。
// 维护节奏：upstream 改 aliRatios 时，把对应 entry 同步到这里。
const HAPPYHORSE_RESOLUTION_RATIOS = { '720P': 1, '1080P': 1.333333 };
const MINIMAX_H3_RESOLUTION_RATIOS = { '768P': 1, '1080P': 1.25, '2K': 1.25 };

const RESOLUTION_RATIOS = {
  'MiniMax-H3': MINIMAX_H3_RESOLUTION_RATIOS,
  'happyhorse-1.0-t2v': HAPPYHORSE_RESOLUTION_RATIOS,
  'happyhorse-1.0-i2v': HAPPYHORSE_RESOLUTION_RATIOS,
  'happyhorse-1.0-r2v': HAPPYHORSE_RESOLUTION_RATIOS,
  'happyhorse-1.0-video-edit': HAPPYHORSE_RESOLUTION_RATIOS,
  'happyhorse-1.1-t2v': HAPPYHORSE_RESOLUTION_RATIOS,
  'happyhorse-1.1-i2v': HAPPYHORSE_RESOLUTION_RATIOS,
  'happyhorse-1.1-r2v': HAPPYHORSE_RESOLUTION_RATIOS,
};

export function resolveResolutionRatios(modelName) {
  return RESOLUTION_RATIOS[modelName] || null;
}

// 按 newapi 后端 relay/channel/task/hailuo/constants.go HailuoTierRatios 复刻的"分辨率 × 时长"分档倍率。
// 维护节奏：upstream 改 HailuoTierRatios 时同步修改。tier key: "<Resolution>-<Duration>" 如 "768P-6"。
const HAILUO_TIER_RATIOS = {
  'MiniMax-Hailuo-2.3-Fast': {
    '768P-6': 1.0,
    '768P-10': 2.25 / 1.35,
    '1080P-6': 2.31 / 1.35,
  },
  'MiniMax-Hailuo-2.3': {
    '768P-6': 1.0,
    '768P-10': 2.0,
    '1080P-6': 1.75,
  },
  'MiniMax-Hailuo-02': {
    '768P-6': 1.0,
    '768P-10': 2.0,
    '1080P-6': 1.75,
    '512P-6': 0.3,
    '512P-10': 0.5,
  },
};

export function resolveHailuoTierRatios(modelName) {
  return HAILUO_TIER_RATIOS[modelName] || null;
}

// 给 per_video_tier 模型按档位算出每档绝对价（含 group_ratio）。
// 返回 [{ tier: '768P-6', resolution: '768P', duration: 6, ratio: 1.0, priceLabel: '¥1.35' }, ...]
export function resolveVideoTierRows(model, usedGroupRatio, displayPrice) {
  const tiers = resolveHailuoTierRatios(model?.model_name);
  if (!tiers) return null;
  const basePrice = Number(model.model_price) || 0;
  const gr = Number(usedGroupRatio) || 1;
  return Object.entries(tiers).map(([tier, ratio]) => {
    const [resolution, durationStr] = tier.split('-');
    const priceUSD = basePrice * ratio * gr;
    return {
      tier,
      resolution,
      duration: Number(durationStr),
      ratio,
      priceLabel:
        typeof displayPrice === 'function'
          ? displayPrice(priceUSD)
          : `${priceUSD}`,
    };
  });
}

// Seedance 实际按输出 token 计费。为了让模型广场能直接回答“每秒大约多少钱”，
// 这里用官方 16:9、5 秒样例的 token 用量换算预估秒价；后端仍按真实 token 精确结算。
// 官方 CNY/1M token 单价与 relay/channel/task/doubao/constants.go 必须同步。
const SEEDANCE_VIDEO_TOKEN_PRICES = {
  'doubao-seedance-2-5-260628': [
    {
      resolution: '480P',
      sampleDuration: 5,
      sampleTokenUsage: 48038,
      tokensPerSecond: 9607.6,
      withoutVideo: 70,
      withVideo: 42,
    },
    {
      resolution: '720P',
      sampleDuration: 5,
      sampleTokenUsage: 108000,
      tokensPerSecond: 21600,
      withoutVideo: 70,
      withVideo: 42,
    },
    {
      resolution: '1080P',
      sampleDuration: 5,
      sampleTokenUsage: 243000,
      tokensPerSecond: 48600,
      withoutVideo: 77,
      withVideo: 46,
    },
  ],
  'doubao-seedance-2-0-260128': [
    {
      resolution: '480P',
      sampleDuration: 5,
      sampleTokenUsage: 50220,
      tokensPerSecond: 10044,
      withoutVideo: 46,
      withVideo: 28,
    },
    {
      resolution: '720P',
      sampleDuration: 5,
      sampleTokenUsage: 108000,
      tokensPerSecond: 21600,
      withoutVideo: 46,
      withVideo: 28,
    },
    {
      resolution: '1080P',
      sampleDuration: 5,
      sampleTokenUsage: 243000,
      tokensPerSecond: 48600,
      withoutVideo: 51,
      withVideo: 31,
    },
    {
      resolution: '4K',
      sampleDuration: 5,
      sampleTokenUsage: 972000,
      tokensPerSecond: 194400,
      withoutVideo: 26,
      withVideo: 16,
    },
  ],
  'doubao-seedance-2-0-fast-260128': [
    {
      resolution: '480P',
      sampleDuration: 5,
      sampleTokenUsage: 50220,
      tokensPerSecond: 10044,
      withoutVideo: 37,
      withVideo: 22,
    },
    {
      resolution: '720P',
      sampleDuration: 5,
      sampleTokenUsage: 108000,
      tokensPerSecond: 21600,
      withoutVideo: 37,
      withVideo: 22,
    },
  ],
  'doubao-seedance-2-0-mini-260615': [
    {
      resolution: '480P',
      sampleDuration: 5,
      sampleTokenUsage: 50220,
      tokensPerSecond: 10044,
      withoutVideo: 23,
      withVideo: 14,
    },
    {
      resolution: '720P',
      sampleDuration: 5,
      sampleTokenUsage: 108000,
      tokensPerSecond: 21600,
      withoutVideo: 23,
      withVideo: 14,
    },
  ],
};

export function resolveSeedanceVideoPriceRows(
  model,
  usedGroupRatio,
  displayCnyPrice,
) {
  const rows = SEEDANCE_VIDEO_TOKEN_PRICES[model?.model_name];
  if (!rows) return null;
  const gr = Number(usedGroupRatio) || 1;
  return rows.map((row) => {
    const sampleDuration = row.sampleDuration || 5;
    const sampleTokenUsage =
      row.sampleTokenUsage || Math.round(row.tokensPerSecond * sampleDuration);
    const withoutVideoPerSecond =
      (row.withoutVideo * row.tokensPerSecond * gr) / 1000000;
    const withVideoPerSecond =
      (row.withVideo * row.tokensPerSecond * gr) / 1000000;
    return {
      ...row,
      sampleDuration,
      sampleTokenUsage,
      withoutVideoPriceLabel:
        typeof displayCnyPrice === 'function'
          ? displayCnyPrice(withoutVideoPerSecond)
          : `¥${withoutVideoPerSecond.toFixed(3)}`,
      withVideoPriceLabel:
        typeof displayCnyPrice === 'function'
          ? displayCnyPrice(withVideoPerSecond)
          : `¥${withVideoPerSecond.toFixed(3)}`,
      withoutVideoCostLabel:
        typeof displayCnyPrice === 'function'
          ? displayCnyPrice(withoutVideoPerSecond * sampleDuration)
          : `¥${(withoutVideoPerSecond * sampleDuration).toFixed(3)}`,
      withVideoCostLabel:
        typeof displayCnyPrice === 'function'
          ? displayCnyPrice(withVideoPerSecond * sampleDuration)
          : `¥${(withVideoPerSecond * sampleDuration).toFixed(3)}`,
    };
  });
}

// 给 per_second 模型按分辨率倍率算出每档绝对价（含 group_ratio）。
// displayPrice 由调用方注入（带 currency / 充值汇率处理），保持单一 formatter。
// 返回 [{ resolution: '720P', ratio: 1, priceLabel: '¥1.530' }, ...] 或 null。
export function resolveResolutionPriceRows(
  model,
  usedGroupRatio,
  displayPrice,
) {
  const ratios = resolveResolutionRatios(model?.model_name);
  if (!ratios) return null;
  const basePrice = Number(model.model_price) || 0;
  const gr = Number(usedGroupRatio) || 1;
  return Object.entries(ratios).map(([resolution, ratio]) => {
    const priceUSD = basePrice * ratio * gr;
    return {
      resolution,
      ratio,
      priceLabel:
        typeof displayPrice === 'function'
          ? displayPrice(priceUSD)
          : `${priceUSD}`,
    };
  });
}

// per_character 模型的字符价常数（与 newapi 后端 relay/helper/price.go 的 RPL/charRatio 对齐）
const CHARACTER_PRICE_CONST = 0.002;

export const PRICING_TYPES = {
  PER_TOKEN: 'per_token',
  PER_CALL: 'per_call',
  PER_SECOND: 'per_second',
  PER_MINUTE: 'per_minute',
  PER_CHARACTER: 'per_character',
  PER_VIDEO_TIER: 'per_video_tier',
  PER_VIDEO_TOKEN: 'per_video_token',
};

export const PRICING_TEMPLATES = {
  per_token: {
    type: 'per_token',
    label: '按量计费',
    unit: 'token',
    factors: ['model_ratio', 'completion_ratio', 'group_ratio'],
    formula: '输入单价 × 输入token + 输出单价 × 输出token',
    renderFormula: () => null,
  },
  per_call: {
    type: 'per_call',
    label: '按次计费',
    unit: '次',
    factors: ['model_price', 'group_ratio'],
    formula: '模型单价 × 分组倍率',
    renderFormula: (model, groupRatio) => {
      const gr = Number(groupRatio) || 1;
      const price = Number(model.model_price) || 0;
      return {
        formula: `${formatNumber(price)} 元 × 分组倍率(${formatNumber(gr)})`,
        examples: [
          `单次调用：${formatNumber(price)} × ${formatNumber(gr)} = ${formatNumber(price * gr, 2)} 元`,
        ],
      };
    },
  },
  per_second: {
    type: 'per_second',
    label: '按秒计费',
    unit: '秒',
    factors: ['model_price', 'duration', 'resolution_ratio', 'group_ratio'],
    formula: '单价(元/秒) × 时长(秒) × 分辨率倍率 × 分组倍率',
    renderFormula: (model, groupRatio) => {
      const gr = Number(groupRatio) || 1;
      const price = Number(model.model_price) || 0;
      const ratios = resolveResolutionRatios(model.model_name);
      const examples = [];
      if (ratios) {
        Object.entries(ratios).forEach(([res, r]) => {
          examples.push(
            `${res} 5秒：${formatNumber(price)} × 5 × ${formatNumber(r)} × ${formatNumber(gr)} = ${formatNumber(price * 5 * r * gr, 2)} 元`,
          );
        });
      } else {
        examples.push(
          `5秒：${formatNumber(price)} × 5 × ${formatNumber(gr)} = ${formatNumber(price * 5 * gr, 2)} 元（无分辨率倍率表）`,
        );
      }
      return {
        formula: `${formatNumber(price)} 元/秒 × 时长 × 分辨率倍率 × 分组倍率(${formatNumber(gr)})`,
        resolutionTable: ratios,
        examples,
      };
    },
  },
  per_minute: {
    type: 'per_minute',
    label: '按分钟计费',
    unit: '分钟',
    factors: ['model_price', 'duration', 'group_ratio'],
    formula: '单价(元/分钟) × 分钟数 × 分组倍率',
    renderFormula: (model, groupRatio) => {
      const gr = Number(groupRatio) || 1;
      const price = Number(model.model_price) || 0;
      return {
        formula: `${formatNumber(price)} 元/分钟 × 分钟数 × 分组倍率(${formatNumber(gr)})`,
        examples: [
          `1 分钟：${formatNumber(price)} × 1 × ${formatNumber(gr)} = ${formatNumber(price * gr, 2)} 元`,
          `5 分钟：${formatNumber(price)} × 5 × ${formatNumber(gr)} = ${formatNumber(price * 5 * gr, 2)} 元`,
        ],
      };
    },
  },
  per_video_tier: {
    type: 'per_video_tier',
    label: '按视频档位计费',
    unit: '次',
    factors: ['model_price', 'tier_ratio', 'group_ratio'],
    formula: 'ModelPrice(基准价) × 档位倍率(分辨率+时长) × 分组倍率',
    renderFormula: (model, groupRatio) => {
      const gr = Number(groupRatio) || 1;
      const price = Number(model.model_price) || 0;
      const tiers = resolveHailuoTierRatios(model.model_name);
      if (!tiers) return null;
      const examples = Object.entries(tiers).map(([tier, r]) => {
        const [res, dur] = tier.split('-');
        return `${res} ${dur}秒：${formatNumber(price)} × ${formatNumber(r)} × ${formatNumber(gr)} = ${formatNumber(price * r * gr, 2)} 元`;
      });
      return {
        formula: `${formatNumber(price)} 元(768P 6s 基准) × 档位倍率 × 分组倍率(${formatNumber(gr)})`,
        note: '档位倍率按 MiniMax 官方价表 (resolution × duration 组合) 配置',
        examples,
      };
    },
  },
  per_video_token: {
    type: 'per_video_token',
    label: '视频 Token 计费',
    unit: '秒（预估）',
    factors: [
      'model_ratio',
      'output_tokens',
      'video_input_ratio',
      'group_ratio',
    ],
    formula: '官方 Token 单价 × 实际输出 Token 数 × 分组倍率',
    renderFormula: (model, groupRatio) => {
      const gr = Number(groupRatio) || 1;
      const rows = SEEDANCE_VIDEO_TOKEN_PRICES[model.model_name];
      if (!rows) return null;
      return {
        formula: `官方元/百万 Token 单价 × 实际输出 Token ÷ 1,000,000 × 分组倍率(${formatNumber(gr)})`,
        note: '表格按官方 16:9、5 秒、无参考视频样例估算；真实账单严格按上游返回的实际 Token 数结算。含参考视频时使用官方对应 Token 单价。',
        examples: [],
      };
    },
  },
  per_character: {
    type: 'per_character',
    label: '按字符计费',
    unit: '字符',
    factors: ['model_ratio', 'character_count', 'group_ratio'],
    formula: 'ratio × 字符数 × 0.002 × 分组倍率',
    renderFormula: (model, groupRatio) => {
      const gr = Number(groupRatio) || 1;
      const ratio = Number(model.model_ratio) || 0;
      const perChar = ratio * CHARACTER_PRICE_CONST * gr;
      return {
        formula: `${formatNumber(ratio)} × 字符数 × ${CHARACTER_PRICE_CONST} × 分组倍率(${formatNumber(gr)})`,
        note: '汉字按 2 字符计（newapi 后端字符计数规则）',
        examples: [
          `100 字符：${formatNumber(ratio)} × 100 × ${CHARACTER_PRICE_CONST} × ${formatNumber(gr)} = ${formatNumber(perChar * 100, 4)} 元`,
          `1000 字符：${formatNumber(ratio)} × 1000 × ${CHARACTER_PRICE_CONST} × ${formatNumber(gr)} = ${formatNumber(perChar * 1000, 4)} 元`,
        ],
      };
    },
  },
};

// 模型名 → pricingType 映射规则。
// 顺序敏感：更具体的规则在前（如 viapi 单模型 match 在通用前缀前）。
export const MODEL_PRICING_RULES = [
  // 按分钟视频处理（viapi 家族 — fulladaptor 引入，精确匹配）
  { match: (n) => n === 'viapi-super-resolve', type: PRICING_TYPES.PER_MINUTE },
  {
    match: (n) => n === 'viapi-erase-subtitles',
    type: PRICING_TYPES.PER_MINUTE,
  },

  // 按次（viapi 其他单次操作）
  { match: (n) => n === 'viapi-segment-common', type: PRICING_TYPES.PER_CALL },

  // 按秒视频（happyhorse 家族 — fulladaptor 引入）
  { match: (n) => n.startsWith('happyhorse-'), type: PRICING_TYPES.PER_SECOND },
  // MiniMax H3 是按秒 × 分辨率倍率；必须放在旧 Hailuo 前缀规则之前。
  { match: (n) => n === 'MiniMax-H3', type: PRICING_TYPES.PER_SECOND },
  // 按档位视频（MiniMax Hailuo 家族 — 分辨率+时长 组合查表）
  {
    match: (n) => n.startsWith('MiniMax-Hailuo-'),
    type: PRICING_TYPES.PER_VIDEO_TIER,
  },
  // Seedance 实际按视频输出 token 结算；模型广场额外提供透明的预估秒价。
  {
    match: (n) => n.startsWith('doubao-seedance-'),
    type: PRICING_TYPES.PER_VIDEO_TOKEN,
  },

  // 按字符 TTS
  { match: (n) => n.startsWith('speech-'), type: PRICING_TYPES.PER_CHARACTER },

  // 按次（MiniMax 全家桶 — fulladaptor 引入）
  { match: (n) => n.startsWith('music-'), type: PRICING_TYPES.PER_CALL },
  { match: (n) => n === 'minimax-voice-clone', type: PRICING_TYPES.PER_CALL },
];

export function resolvePricingType(modelName, quotaType) {
  if (!modelName) {
    return quotaType === 0 ? PRICING_TYPES.PER_TOKEN : PRICING_TYPES.PER_CALL;
  }
  const matched = MODEL_PRICING_RULES.find((r) => r.match(modelName));
  if (matched) return matched.type;
  // Fallback：保持现状语义
  return quotaType === 0 ? PRICING_TYPES.PER_TOKEN : PRICING_TYPES.PER_CALL;
}

export function resolvePricingTemplate(modelName, quotaType) {
  return PRICING_TEMPLATES[resolvePricingType(modelName, quotaType)];
}
