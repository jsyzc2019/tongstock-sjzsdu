import { useEffect, useState } from 'react';
import { Alert, Drawer, Space, Table, Tag, Typography, message } from 'antd';
import type { MethodCard, MethodPoolResult } from '../../api/client';
import { api } from '../../api/client';

const { Text } = Typography;

interface MethodPoolDrawerProps {
  open: boolean;
  method: MethodCard | null;
  onClose: () => void;
}

/** 方法股票池：当前冻结快照上逐只解析 scope，池内股票标注当日是否触发。 */
export default function MethodPoolDrawer({ open, method, onClose }: MethodPoolDrawerProps) {
  const [pool, setPool] = useState<MethodPoolResult>();
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');

  useEffect(() => {
    if (!open || !method) return;
    let cancelled = false;
    setLoading(true);
    setError('');
    setPool(undefined);
    api
      .methodPool(method.id)
      .then((res) => {
        if (!cancelled) setPool(res);
      })
      .catch((e: unknown) => {
        if (!cancelled) setError(e instanceof Error ? e.message : '股票池读取失败');
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [open, method]);

  const fmtNum = (v?: number, digits = 2) => (v === undefined || v === 0 ? '—' : v.toFixed(digits));

  return (
    <Drawer
      title={`股票池 · ${method?.name ?? ''}`}
      open={open}
      onClose={onClose}
      width={720}
      destroyOnHidden
    >
      {error && <Alert type="error" showIcon message="股票池读取失败" description={error} />}
      {pool && (
        <Space direction="vertical" size={12} style={{ display: 'flex' }}>
          <Space size={8} wrap>
            <Tag>快照日 {pool.snapshot_date}</Tag>
            <Tag color="blue">扫描 {pool.total} 只</Tag>
            <Tag color="cyan">池内 {pool.in_scope_count} 只</Tag>
            <Tag color="red">当日触发 {pool.triggered_count} 只</Tag>
          </Space>
          <Text type="secondary" style={{ fontSize: 12 }}>
            池内 = 落在方法 scope（股票池/市值/户均持股/ST 过滤）内；触发 = 当日入场规则命中。数据为最新冻结快照的现值。
          </Text>
          <Table
            size="small"
            loading={loading}
            rowKey="code"
            dataSource={pool.entries}
            pagination={{ pageSize: 20, showSizeChanger: false, showTotal: (t) => `共 ${t} 只` }}
            columns={[
              { title: '代码', dataIndex: 'code', width: 90, render: (v: string) => <Text code>{v}</Text> },
              { title: '名称', dataIndex: 'name', width: 110, ellipsis: true },
              {
                title: '收盘', dataIndex: 'close', width: 90, align: 'right',
                render: (v?: number) => fmtNum(v),
              },
              {
                title: '市值(亿)', dataIndex: 'market_cap', width: 100, align: 'right',
                render: (v?: number) => fmtNum(v, 1),
                sorter: (a, b) => (a.market_cap ?? 0) - (b.market_cap ?? 0),
              },
              {
                title: '户均(股)', dataIndex: 'avg_shares_per_holder', width: 100, align: 'right',
                render: (v?: number) => (v ? Math.round(v).toLocaleString() : '—'),
                sorter: (a, b) => (a.avg_shares_per_holder ?? 0) - (b.avg_shares_per_holder ?? 0),
              },
              {
                title: '状态', dataIndex: 'in_scope', width: 100,
                filters: [
                  { text: '当日触发', value: 'triggered' },
                  { text: '池内未触发', value: 'in' },
                  { text: '被排除', value: 'out' },
                ],
                onFilter: (value, record) => {
                  if (value === 'triggered') return record.in_scope && record.triggered;
                  if (value === 'in') return record.in_scope && !record.triggered;
                  return !record.in_scope;
                },
                render: (_: unknown, record) => {
                  if (!record.in_scope) {
                    return <Tag>排除{record.reason_code ? ` · ${record.reason_code}` : ''}</Tag>;
                  }
                  return record.triggered
                    ? <Tag color="red">当日触发</Tag>
                    : <Tag color="default">池内</Tag>;
                },
              },
              {
                title: '说明', dataIndex: 'detail', ellipsis: true,
                render: (v?: string) => v ? <Text type="secondary" style={{ fontSize: 12 }}>{v}</Text> : null,
              },
            ]}
          />
        </Space>
      )}
      {!pool && !error && loading && <Table size="small" loading rowKey="code" dataSource={[]} columns={[{ title: '代码' }, { title: '名称' }, { title: '状态' }]} />}
      {!pool && !error && !loading && method && (
        <Text type="secondary" onClick={() => message.info('等待数据')}>正在准备股票池数据…</Text>
      )}
    </Drawer>
  );
}
