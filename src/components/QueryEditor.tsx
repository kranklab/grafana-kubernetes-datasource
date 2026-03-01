import React, { ChangeEvent } from 'react';
import { InlineField, Input, Stack } from '@grafana/ui';
import { QueryEditorProps } from '@grafana/data';
import { DataSource } from '../datasource';
import { KubernetesDatasourceOptions, KubernetesQuery } from '../types';

type Props = QueryEditorProps<DataSource, KubernetesQuery, KubernetesDatasourceOptions>;

export function QueryEditor({ query, onChange, onRunQuery }: Props) {
  const isGet = query.action === 'get';

  const update = (patch: Partial<KubernetesQuery>) => {
    onChange({ ...query, ...patch });
    onRunQuery();
  };

  return (
    <Stack gap={0}>
      <InlineField label="Action">
        <Input
          id="query-editor-action"
          onChange={(e: ChangeEvent<HTMLInputElement>) => update({ action: e.target.value })}
          value={query.action || ''}
          placeholder="list / get / summary"
          width={12}
        />
      </InlineField>

      <InlineField label="Namespace">
        <Input
          id="query-editor-namespace"
          onChange={(e: ChangeEvent<HTMLInputElement>) => update({ namespace: e.target.value })}
          value={query.namespace || ''}
          placeholder="default"
          width={16}
        />
      </InlineField>

      <InlineField label="Resource">
        <Input
          id="query-editor-resource"
          onChange={(e: ChangeEvent<HTMLInputElement>) => update({ resource: e.target.value })}
          value={query.resource || ''}
          placeholder="pods / deployments / ..."
          width={16}
        />
      </InlineField>

      {isGet && (
        <InlineField label="Name">
          <Input
            id="query-editor-name"
            onChange={(e: ChangeEvent<HTMLInputElement>) => update({ name: e.target.value })}
            value={query.name || ''}
            placeholder="resource name"
            width={20}
          />
        </InlineField>
      )}
    </Stack>
  );
}
