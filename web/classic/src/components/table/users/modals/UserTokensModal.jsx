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

import React, { useEffect, useMemo, useState } from 'react';
import { Empty, SideSheet, Space, Tag, Typography } from '@douyinfe/semi-ui';
import {
  IllustrationNoResult,
  IllustrationNoResultDark,
} from '@douyinfe/semi-illustrations';
import { API, showError } from '../../../../helpers';
import { renderGroup, renderQuota } from '../../../../helpers/render';
import { timestamp2string } from '../../../../helpers/utils';
import { useIsMobile } from '../../../../hooks/common/useIsMobile';
import CardTable from '../../../common/ui/CardTable';

const { Text } = Typography;

// Mirrors TokensColumnDefs.jsx's renderStatus so the admin read-only view
// matches the existing token management page's status semantics.
function renderStatusTag(status, t) {
  let tagColor = 'black';
  let tagText = t('未知状态');
  if (status === 1) {
    tagColor = 'green';
    tagText = t('已启用');
  } else if (status === 2) {
    tagColor = 'red';
    tagText = t('已禁用');
  } else if (status === 3) {
    tagColor = 'yellow';
    tagText = t('已过期');
  } else if (status === 4) {
    tagColor = 'grey';
    tagText = t('已耗尽');
  }
  return (
    <Tag color={tagColor} shape='circle' size='small'>
      {tagText}
    </Tag>
  );
}

function renderQuotaCell(record, t) {
  if (record?.unlimited_quota) {
    return (
      <Tag color='white' shape='circle'>
        {t('无限额度')}
      </Tag>
    );
  }
  const remain = Number(record?.remain_quota || 0);
  const used = Number(record?.used_quota || 0);
  const total = remain + used;
  return (
    <Text type={total > 0 ? 'secondary' : 'tertiary'}>
      {`${renderQuota(remain)} / ${renderQuota(total)}`}
    </Text>
  );
}

const DEFAULT_PAGE_SIZE = 10;

const UserTokensModal = ({ visible, onCancel, user, t }) => {
  const isMobile = useIsMobile();
  const [loading, setLoading] = useState(false);
  const [tokens, setTokens] = useState([]);
  const [currentPage, setCurrentPage] = useState(1);
  const [pageSize, setPageSize] = useState(DEFAULT_PAGE_SIZE);
  const [total, setTotal] = useState(0);

  const loadUserTokens = async (page = 1, size = pageSize) => {
    if (!user?.id) return;
    setLoading(true);
    try {
      const res = await API.get(
        `/api/token/admin/users/${user.id}/tokens?p=${page}&size=${size}`,
      );
      if (res.data?.success) {
        const data = res.data.data || {};
        setTokens(data.items || []);
        setTotal(data.total || 0);
        setCurrentPage(data.page || page);
        setPageSize(data.page_size || size);
      } else {
        showError(res.data?.message || t('加载失败'));
      }
    } catch (e) {
      showError(t('请求失败'));
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    if (!visible) return;
    setCurrentPage(1);
    loadUserTokens(1, DEFAULT_PAGE_SIZE);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [visible, user?.id]);

  const handlePageChange = (page) => {
    loadUserTokens(page, pageSize);
  };

  const columns = useMemo(() => {
    return [
      {
        title: t('名称'),
        dataIndex: 'name',
      },
      {
        title: t('状态'),
        dataIndex: 'status',
        render: (status) => renderStatusTag(status, t),
      },
      {
        title: t('剩余额度/总额度'),
        key: 'quota_usage',
        render: (_, record) => renderQuotaCell(record, t),
      },
      {
        title: t('分组'),
        dataIndex: 'group',
        render: (text) => <div>{renderGroup(text)}</div>,
      },
      {
        title: t('密钥'),
        dataIndex: 'key',
        render: (text) => <Text code>{text ? `sk-${text}` : '-'}</Text>,
      },
      {
        title: t('创建时间'),
        dataIndex: 'created_time',
        render: (text) => (text ? timestamp2string(text) : '-'),
      },
    ];
  }, [t]);

  return (
    <SideSheet
      visible={visible}
      placement='right'
      width={isMobile ? '100%' : 920}
      bodyStyle={{ padding: 0 }}
      onCancel={onCancel}
      title={
        <Space>
          <Tag color='blue' shape='circle'>
            {t('查看')}
          </Tag>
          <Typography.Title heading={4} className='m-0'>
            {t('令牌管理')}
          </Typography.Title>
          <Text type='tertiary' className='ml-2'>
            {user?.username || '-'} (ID: {user?.id || '-'})
          </Text>
        </Space>
      }
    >
      <div className='p-4'>
        <CardTable
          columns={columns}
          dataSource={tokens}
          rowKey={(row) => row?.id}
          loading={loading}
          scroll={{ x: 'max-content' }}
          hidePagination={false}
          pagination={{
            currentPage,
            pageSize,
            total,
            pageSizeOpts: [10, 20, 50],
            showSizeChanger: false,
            onPageChange: handlePageChange,
          }}
          empty={
            <Empty
              image={
                <IllustrationNoResult style={{ width: 150, height: 150 }} />
              }
              darkModeImage={
                <IllustrationNoResultDark style={{ width: 150, height: 150 }} />
              }
              description={t('暂无密钥数据')}
              style={{ padding: 30 }}
            />
          }
          size='middle'
        />
      </div>
    </SideSheet>
  );
};

export default UserTokensModal;
