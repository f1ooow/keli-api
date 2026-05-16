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

import React, { useState, useEffect } from 'react';
import { Banner, Collapse, Typography, Tag } from '@douyinfe/semi-ui';
import { useTranslation } from 'react-i18next';

const { Text } = Typography;

// localStorage key 持久化用户折叠态选择
const STORAGE_KEY = 'pricing_guide_collapsed';

// 4 种主要计费类型的配置指引（按 PRD R3 段落）+ token 兜底说明
const GUIDE_SECTIONS = [
  {
    type: '按秒计费',
    tagColor: 'cyan',
    example: 'happyhorse-1.0-t2v / hailuo-* / doubao-seedance-*',
    items: [
      '✓ 在【模型固定价格 ModelPrice】配【单价 元/秒】（如 happyhorse-1.0-t2v = 0.9）',
      '✓ 阿里 / 阿里云家族需在 relay/channel/task/ali/adaptor.go 的 aliRatios 加分辨率倍率（720P=1, 1080P=1.778）',
      '✗ 不要配 ModelRatio',
      '✗ 不要在前端额外加映射（pricingTypeRegistry 已按前缀 happyhorse-* 自动识别）',
    ],
  },
  {
    type: '按字符计费',
    tagColor: 'orange',
    example: 'speech-2.8-hd 等 MiniMax 系列 TTS',
    items: [
      '✓ 在【模型倍率 ModelRatio】配【ratio 值】（如 speech-2.8-hd = 1.75）',
      '✗ 不要配 ModelPrice',
      '✗ 公式常数 0.002 是 newapi 后端字符计费规则的固定值，不要改',
    ],
  },
  {
    type: '按分钟计费',
    tagColor: 'green',
    example: 'viapi-super-resolve / viapi-erase-subtitles',
    items: [
      '✓ 在【模型固定价格 ModelPrice】配【单价 元/分钟】',
      '✓ adaptor 的 EstimateBilling 返回 {seconds: <预扣秒数>}',
      '✗ 不要配 ModelRatio',
    ],
  },
  {
    type: '按次计费',
    tagColor: 'teal',
    example: 'music-2.6 / minimax-voice-clone / viapi-segment-common',
    items: [
      '✓ 在【模型固定价格 ModelPrice】配【单价 元/次】',
      '✓ adaptor 的 EstimateBilling 返回空 map',
      '✗ 不要配 ModelRatio',
    ],
  },
  {
    type: '按量计费 (token)',
    tagColor: 'violet',
    example: 'gpt-* / claude-* / gemini-* 等 chat 类',
    items: [
      '✓ 在【模型倍率 ModelRatio】配【ratio 值】（沿用现有约定）',
      '✓ 在【补全倍率 CompletionRatio】配输出/输入比例',
      '说明：本指南不改动 token 计费现状',
    ],
  },
];

const BillingTypeGuideCallout = () => {
  const { t } = useTranslation();
  const [collapsed, setCollapsed] = useState(() => {
    try {
      return localStorage.getItem(STORAGE_KEY) === '1';
    } catch (e) {
      return false;
    }
  });

  useEffect(() => {
    try {
      localStorage.setItem(STORAGE_KEY, collapsed ? '1' : '0');
    } catch (e) {
      // ignore
    }
  }, [collapsed]);

  return (
    <Banner
      type='info'
      fullMode={false}
      closeIcon={null}
      icon={null}
      style={{ marginBottom: 12, borderRadius: 8 }}
      description={
        <Collapse
          activeKey={collapsed ? [] : ['guide']}
          onChange={(keys) => setCollapsed(!keys || keys.length === 0)}
          expandIconPosition='left'
        >
          <Collapse.Panel
            header={
              <span style={{ fontWeight: 600 }}>
                {t('新增模型怎么算钱？5 种计费方式配置指南')}
              </span>
            }
            itemKey='guide'
          >
            <div className='space-y-3'>
              {GUIDE_SECTIONS.map((section, idx) => (
                <div
                  key={idx}
                  className='border-l-2 pl-3 py-1'
                  style={{ borderColor: `var(--semi-color-${section.tagColor}-3)` }}
                >
                  <div className='mb-1'>
                    <Tag color={section.tagColor} shape='circle' size='small'>
                      {t(section.type)}
                    </Tag>
                    <Text type='tertiary' size='small' className='ml-2'>
                      {t('示例模型')}: {section.example}
                    </Text>
                  </div>
                  <ul className='text-xs text-gray-700 space-y-0.5 pl-4'>
                    {section.items.map((line, i) => (
                      <li key={i}>{t(line)}</li>
                    ))}
                  </ul>
                </div>
              ))}
              <Text type='tertiary' size='small' className='block mt-2'>
                {t(
                  '完整流程：先在 newapi UI 编辑 ModelPrice/ModelRatio JSON → 再 commit/部署后端 adaptor 改动（如需新供应商）→ 最后在前端 pricingTypeRegistry.js 加 model_name 前缀映射（如需新 pricingType）。',
                )}
              </Text>
            </div>
          </Collapse.Panel>
        </Collapse>
      }
    />
  );
};

export default BillingTypeGuideCallout;
