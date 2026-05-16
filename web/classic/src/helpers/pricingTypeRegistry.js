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
// 推导出 5 种 pricingType: per_token / per_call / per_second / per_minute / per_character。
// 未匹配模型走 quota_type=0/1 fallback，与改造前 100% 行为一致。

const formatNumber = (n, digits = 4) => {
  if (n === null || n === undefined || Number.isNaN(Number(n))) return '-';
  const num = Number(n);
  if (!Number.isFinite(num)) return '-';
  return Number(num.toFixed(digits)).toString();
};

// 按 newapi 后端 relay/channel/task/ali/adaptor.go aliRatios 复刻的分辨率倍率。
// 维护节奏：upstream 改 aliRatios 时，把对应 entry 同步到这里。
const RESOLUTION_RATIOS = {
  'happyhorse-1.0-t2v': { '720P': 1, '1080P': 1.778 },
  'happyhorse-1.0-i2v': { '720P': 1, '1080P': 1.778 },
  'happyhorse-1.0-r2v': { '720P': 1, '1080P': 1.778 },
  'happyhorse-1.0-video-edit': { '720P': 1, '1080P': 1.778 },
};

export function resolveResolutionRatios(modelName) {
  return RESOLUTION_RATIOS[modelName] || null;
}

// 给 per_second 模型按分辨率倍率算出每档绝对价（含 group_ratio）。
// displayPrice 由调用方注入（带 currency / 充值汇率处理），保持单一 formatter。
// 返回 [{ resolution: '720P', ratio: 1, priceLabel: '¥1.530' }, ...] 或 null。
export function resolveResolutionPriceRows(model, usedGroupRatio, displayPrice) {
  const ratios = resolveResolutionRatios(model?.model_name);
  if (!ratios) return null;
  const basePrice = Number(model.model_price) || 0;
  const gr = Number(usedGroupRatio) || 1;
  return Object.entries(ratios).map(([resolution, ratio]) => {
    const priceUSD = basePrice * ratio * gr;
    return {
      resolution,
      ratio,
      priceLabel: typeof displayPrice === 'function' ? displayPrice(priceUSD) : `${priceUSD}`,
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
          `单次调用：${formatNumber(price)} × ${formatNumber(gr)} = ${formatNumber(price * gr)} 元`,
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
            `${res} 5秒：${formatNumber(price)} × 5 × ${formatNumber(r)} × ${formatNumber(gr)} = ${formatNumber(price * 5 * r * gr)} 元`,
          );
        });
      } else {
        examples.push(
          `5秒：${formatNumber(price)} × 5 × ${formatNumber(gr)} = ${formatNumber(price * 5 * gr)} 元（无分辨率倍率表）`,
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
          `1 分钟：${formatNumber(price)} × 1 × ${formatNumber(gr)} = ${formatNumber(price * gr)} 元`,
          `5 分钟：${formatNumber(price)} × 5 × ${formatNumber(gr)} = ${formatNumber(price * 5 * gr)} 元`,
        ],
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
          `100 字符：${formatNumber(ratio)} × 100 × ${CHARACTER_PRICE_CONST} × ${formatNumber(gr)} = ${formatNumber(perChar * 100)} 元`,
          `1000 字符：${formatNumber(ratio)} × 1000 × ${CHARACTER_PRICE_CONST} × ${formatNumber(gr)} = ${formatNumber(perChar * 1000)} 元`,
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
  { match: (n) => n === 'viapi-erase-subtitles', type: PRICING_TYPES.PER_MINUTE },

  // 按次（viapi 其他单次操作）
  { match: (n) => n === 'viapi-segment-common', type: PRICING_TYPES.PER_CALL },

  // 按秒视频（happyhorse 家族 — fulladaptor 引入）
  { match: (n) => n.startsWith('happyhorse-'), type: PRICING_TYPES.PER_SECOND },
  // 预留：未来 Hailuo / Seedance / Wan 视频按秒计费时在此添加：
  // { match: (n) => n.startsWith('hailuo-'), type: PRICING_TYPES.PER_SECOND },
  // { match: (n) => n.startsWith('doubao-seedance-'), type: PRICING_TYPES.PER_SECOND },

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
