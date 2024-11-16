import { DataSourceInstanceSettings, CoreApp, ScopedVars } from '@grafana/data';
import { DataSourceWithBackend } from '@grafana/runtime';

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
    };
  }

  filterQuery(query: KubernetesQuery): boolean {
    // if no query has been provided, prevent the query from being executed
    return !!query.namespace;
  }
}
