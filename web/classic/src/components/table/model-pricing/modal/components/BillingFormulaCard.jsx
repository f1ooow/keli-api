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

import React from 'react';
import { Avatar, Card, Tag, Typography } from '@douyinfe/semi-ui';
import { IconCalendarClock } from '@douyinfe/semi-icons';

const { Text } = Typography;

// 详情弹窗内的"计费公式说明"卡片。
// 当 pricing_template.renderFormula 返回 null（per_token）时整体不渲染，
// 弹窗保持现有"分组价格"表的样子。
const BillingFormulaCard = ({ model, usedGroupRatio, t }) => {
  const template = model?.pricing_template;
  if (!template || typeof template.renderFormula !== 'function') return null;

  const detail = template.renderFormula(model, usedGroupRatio || 1);
  if (!detail) return null;

  return (
    <div className='mt-6'>
      <div className='flex items-center mb-4'>
        <Avatar size='small' color='cyan' className='mr-2 shadow-md'>
          <IconCalendarClock size={16} />
        </Avatar>
        <div>
          <Text className='text-lg font-medium'>{t('计费公式说明')}</Text>
          <div className='text-xs text-gray-600'>
            {t('展示该模型的计费维度、公式和示例')}
          </div>
        </div>
      </div>

      <Card className='!rounded-lg' bodyStyle={{ padding: '12px 16px' }}>
        <div className='space-y-3'>
          <div>
            <Tag color='blue' shape='circle' size='small'>
              {t(template.label)}
            </Tag>
            <span className='ml-2 text-xs text-gray-500'>
              {t('计费单位')}: {t(template.unit)}
            </span>
          </div>

          <div className='font-mono text-sm bg-gray-50 p-2 rounded text-gray-800'>
            {detail.formula}
          </div>

          {detail.note && (
            <Text type='tertiary' size='small'>
              {t(detail.note)}
            </Text>
          )}

          {Array.isArray(detail.examples) && detail.examples.length > 0 && (
            <div>
              <Text strong size='small' className='block mb-1'>
                {t('示例')}
              </Text>
              <div className='space-y-1'>
                {detail.examples.map((ex, i) => (
                  <div
                    key={i}
                    className='text-xs font-mono text-gray-600 bg-gray-50 px-2 py-1 rounded'
                  >
                    {ex}
                  </div>
                ))}
              </div>
            </div>
          )}
        </div>
      </Card>
    </div>
  );
};

export default BillingFormulaCard;
