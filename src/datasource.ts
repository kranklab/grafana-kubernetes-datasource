import { DataSourceInstanceSettings, CoreApp, ScopedVars, MetricFindValue, getDefaultTimeRange } from '@grafana/data';
import { DataSourceWithBackend, getTemplateSrv } from '@grafana/runtime';
import { lastValueFrom } from 'rxjs';

import { KubernetesQuery, KubernetesDatasourceOptions, DEFAULT_QUERY } from './types';

export class DataSource extends DataSourceWithBackend<KubernetesQuery, KubernetesDatasourceOptions> {
  constructor(instanceSettings: DataSourceInstanceSettings<KubernetesDatasourceOptions>) {
    super(instanceSettings);
  }

  getDefaultQuery(_: CoreApp): Partial<KubernetesQuery> {
    return DEFAULT_QUERY;
  }

  applyTemplateVariables(query: KubernetesQuery, scopedVars: ScopedVars) {
    return {
      ...query,
      namespace: getTemplateSrv().replace(query.namespace ?? '', scopedVars),
    };
  }

  filterQuery(query: KubernetesQuery): boolean {
    if (query.action === 'get') {
      return !!query.resource && !!query.name;
    }
    return !!query.action && !!query.resource;
  }

  async metricFindQuery(_query: unknown): Promise<MetricFindValue[]> {
    let response;
    try {
      response = await lastValueFrom(
        this.query({
          requestId: 'namespace-variable',
          app: CoreApp.Unknown,
          timezone: '',
          range: getDefaultTimeRange(),
          interval: '1m',
          intervalMs: 60000,
          maxDataPoints: 1000,
          scopedVars: {},
          targets: [
            {
              refId: 'A',
              action: 'list',
              resource: 'namespaces',
              namespace: '',
            },
          ],
          startTime: Date.now(),
        })
      );
    } catch (err) {
      console.error('Failed to fetch namespaces for variable:', err);
      return [];
    }

    const values: MetricFindValue[] = [];
    for (const frame of response?.data ?? []) {
      const nameField = frame.fields?.find((f: any) => f.name === 'name');
      for (const v of nameField?.values ?? []) {
        if (v != null) {
          values.push({ text: String(v), value: String(v) });
        }
      }
    }
    return values;
  }
}
