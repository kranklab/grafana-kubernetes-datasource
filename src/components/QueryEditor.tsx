import React, { ChangeEvent } from 'react';
import {InlineField, Input, Stack} from '@grafana/ui';
import {QueryEditorProps} from '@grafana/data';
import {DataSource} from '../datasource';
import {KubernetesDatasourceOptions, KubernetesQuery} from '../types';

type Props = QueryEditorProps<DataSource, KubernetesQuery, KubernetesDatasourceOptions>;

export function QueryEditor({query, onChange, onRunQuery}: Props) {

    return (
        <Stack gap={0}>
            <InlineField label="Action">
                <Input
                    id="query-editor-action-text"
                    onChange={(event: ChangeEvent<HTMLInputElement>) => {
                        onChange({
                            ...query,
                            action: event.target.value
                        })
                        onRunQuery()
                    }}
                    value={query.action || ''}
                    required
                    placeholder="Enter a action name"
                />
      </InlineField>
            <InlineField label="Namespace">
                <Input
                    id="query-editor-namespace-text"
                    onChange={(event: ChangeEvent<HTMLInputElement>) => {
                        onChange({
                            ...query,
                            namespace: event.target.value
                        })
                        onRunQuery()
                    }}
                    value={query.namespace || ''}
                    required
                    placeholder="Enter a namespace name"
                />
      </InlineField>
            <InlineField label="Resource">
                <Input
                    id="query-editor-resource-text"
                    onChange={(event: ChangeEvent<HTMLInputElement>) => {
                        onChange({
                            ...query,
                            resource: event.target.value
                        })
                        onRunQuery()
                    }}
                    value={query.resource || ''}
                    required
                    placeholder="Enter a resource name"
                />
      </InlineField>
    </Stack>
  );
}
