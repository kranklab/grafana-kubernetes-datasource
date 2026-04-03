import { DataSourceInstanceSettings, CoreApp, ScopedVars, MetricFindValue, getDefaultTimeRange } from '@grafana/data';
import { DataSourceWithBackend, getTemplateSrv } from '@grafana/runtime';
import { lastValueFrom } from 'rxjs';

import { KubernetesQuery, KubernetesDatasourceOptions, DEFAULT_QUERY } from './types';

export class DataSource extends DataSourceWithBackend<KubernetesQuery, KubernetesDatasourceOptions> {
  defaultNamespace: string;

  constructor(instanceSettings: DataSourceInstanceSettings<KubernetesDatasourceOptions>) {
    super(instanceSettings);
    this.defaultNamespace = instanceSettings.jsonData.defaultNamespace || 'default';
  }

  getDefaultQuery(_: CoreApp): Partial<KubernetesQuery> {
    return { ...DEFAULT_QUERY, namespace: this.defaultNamespace };
  }

  applyTemplateVariables(query: KubernetesQuery, scopedVars: ScopedVars) {
    const ns = getTemplateSrv().replace(query.namespace ?? '', scopedVars);
    return {
      ...query,
      namespace: ns || this.defaultNamespace,
    };
  }

  filterQuery(query: KubernetesQuery): boolean {
    if (query.action === 'get') {
      return !!query.resource && !!query.name;
    }
    return !!query.resource;
  }

  private async listResource(requestId: string, resource: string, namespace: string): Promise<any> {
    return lastValueFrom(
      this.query({
        requestId,
        app: CoreApp.Unknown,
        timezone: '',
        range: getDefaultTimeRange(),
        interval: '1m',
        intervalMs: 60000,
        maxDataPoints: 1000,
        scopedVars: {},
        targets: [{ refId: 'A', action: 'list', resource, namespace }],
        startTime: Date.now(),
      })
    );
  }

  async getNamespaces(): Promise<string[]> {
    let response;
    try {
      response = await this.listResource('namespace-dropdown', 'namespaces', '');
    } catch (err) {
      console.error('Failed to fetch namespaces:', err);
      return [];
    }
    const names: string[] = [];
    for (const frame of response?.data ?? []) {
      const field = frame.fields?.find((f: any) => f.name === 'Name');
      for (const v of field?.values ?? []) {
        if (v != null) { names.push(String(v)); }
      }
    }
    return names;
  }

  async getNodes(): Promise<string[]> {
    let response;
    try {
      response = await this.listResource('node-dropdown', 'nodes', '');
    } catch (err) {
      console.error('Failed to fetch nodes:', err);
      return [];
    }
    const names: string[] = [];
    for (const frame of response?.data ?? []) {
      const field = frame.fields?.find((f: any) => f.name === 'Name');
      for (const v of field?.values ?? []) {
        if (v != null) { names.push(String(v)); }
      }
    }
    return names;
  }

  async getLabels(namespace: string, resource: string): Promise<string[]> {
    if (!resource) { return []; }
    let response;
    try {
      response = await this.listResource('label-dropdown', resource, namespace);
    } catch (err) {
      console.error('Failed to fetch labels:', err);
      return [];
    }
    const labelSet = new Set<string>();
    for (const frame of response?.data ?? []) {
      const field = frame.fields?.find((f: any) => f.name === 'Labels');
      for (const v of field?.values ?? []) {
        if (v == null) { continue; }
        try {
          for (const [key, val] of Object.entries(v)) {
            labelSet.add(`${key}=${val}`);
          }
        } catch {}
      }
    }
    return Array.from(labelSet).sort();
  }

  async metricFindQuery(_query: unknown): Promise<MetricFindValue[]> {
    let response;
    try {
      response = await this.listResource('namespace-variable', 'namespaces', '');
    } catch (err) {
      console.error('Failed to fetch namespaces for variable:', err);
      return [];
    }
    const values: MetricFindValue[] = [];
    for (const frame of response?.data ?? []) {
      const field = frame.fields?.find((f: any) => f.name === 'Name');
      for (const v of field?.values ?? []) {
        if (v != null) { values.push({ text: String(v), value: String(v) }); }
      }
    }
    return values;
  }
}
