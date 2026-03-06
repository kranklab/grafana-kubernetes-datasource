import React from 'react';
import { InlineField, Combobox, Stack } from '@grafana/ui';
import { QueryEditorProps } from '@grafana/data';
import { ComboboxOption } from '@grafana/ui';
import { DataSource } from '../datasource';
import { KubernetesDatasourceOptions, KubernetesQuery } from '../types';

type Props = QueryEditorProps<DataSource, KubernetesQuery, KubernetesDatasourceOptions>;

const RESOURCE_OPTIONS: ComboboxOption[] = [
  { label: 'Pods', value: 'pods' },
  { label: 'Deployments', value: 'deployments' },
  { label: 'DaemonSets', value: 'daemonsets' },
  { label: 'StatefulSets', value: 'statefulsets' },
  { label: 'ReplicaSets', value: 'replicasets' },
  { label: 'Jobs', value: 'jobs' },
  { label: 'CronJobs', value: 'cronjobs' },
  { label: 'Services', value: 'services' },
  { label: 'Ingresses', value: 'ingresses' },
  { label: 'Ingress Classes', value: 'ingressclasses' },
  { label: 'ConfigMaps', value: 'configmaps' },
  { label: 'Secrets', value: 'secrets' },
  { label: 'Persistent Volume Claims', value: 'persistentvolumeclaims' },
  { label: 'Persistent Volumes', value: 'persistentvolumes' },
  { label: 'Storage Classes', value: 'storageclasses' },
  { label: 'Namespaces', value: 'namespaces' },
  { label: 'Nodes', value: 'nodes' },
  { label: 'Events', value: 'events' },
  { label: 'Network Policies', value: 'networkpolicies' },
  { label: 'Service Accounts', value: 'serviceaccounts' },
  { label: 'Roles', value: 'roles' },
  { label: 'Role Bindings', value: 'rolebindings' },
  { label: 'Cluster Roles', value: 'clusterroles' },
  { label: 'Cluster Role Bindings', value: 'clusterrolebindings' },
  { label: 'CRDs', value: 'crds' },
];

export function QueryEditor({ query, onChange, onRunQuery, datasource }: Props) {
  const update = (patch: Partial<KubernetesQuery>) => {
    onChange({ ...query, action: 'list', ...patch });
    onRunQuery();
  };

  const loadNamespaces = async (): Promise<ComboboxOption[]> => {
    const names = await datasource.getNamespaces();
    return [
      { label: 'All namespaces', value: '' },
      ...names.map((n) => ({ label: n, value: n })),
    ];
  };

  const loadNodes = async (): Promise<ComboboxOption[]> => {
    const names = await datasource.getNodes();
    return [
      { label: 'Any node', value: '' },
      ...names.map((n) => ({ label: n, value: n })),
    ];
  };

  const loadLabels = async (): Promise<ComboboxOption[]> => {
    const labels = await datasource.getLabels(query.namespace ?? '', query.resource ?? '');
    return [
      { label: 'No filter', value: '' },
      ...labels.map((l) => ({ label: l, value: l })),
    ];
  };

  return (
    <Stack gap={0}>
      <InlineField label="Namespace">
        <Combobox
          options={loadNamespaces}
          value={query.namespace ?? ''}
          onChange={(v) => update({ namespace: v?.value ?? '' })}
          width={20}
          placeholder="Select Namespace"
        />
      </InlineField>

      <InlineField label="Resource">
        <Combobox
          options={RESOURCE_OPTIONS}
          value={query.resource || ''}
          onChange={(v) => update({ resource: v.value ?? '' })}
          width={24}
          placeholder="Select Resource"
        />
      </InlineField>

      <InlineField label="Label" tooltip="Filter by label key=value. Options load from selected resource.">
        <Combobox
          key={`${query.namespace}-${query.resource}`}
          options={loadLabels}
          value={query.labelSelector ?? ''}
          onChange={(v) => update({ labelSelector: v?.value ?? '' })}
          width={24}
          placeholder="key=value"
          isClearable
        />
      </InlineField>

      <InlineField label="Node" tooltip="Filter pods by node name">
        <Combobox
          options={loadNodes}
          value={query.nodeName ?? ''}
          onChange={(v) => update({ nodeName: v?.value ?? '' })}
          width={20}
          placeholder="Select Node"
          isClearable
        />
      </InlineField>
    </Stack>
  );
}