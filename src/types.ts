import { DataSourceJsonData } from '@grafana/data';
import { DataQuery } from '@grafana/schema';

export interface KubernetesQuery extends DataQuery {
  action: string | 'get' | 'describe';
  namespace: string;
  resource: string | 'pod';
}

export const DEFAULT_QUERY: Partial<KubernetesQuery> = {
  action: "get",
  namespace: 'default',
  resource: 'pod'
};

export interface DataPoint {
  Time: number;
  Value: number;
}

export interface DataSourceResponse {
  datapoints: DataPoint[];
}

/**
 * These are options configured for each DataSource instance
 */
export interface KubernetesDatasourceOptions extends DataSourceJsonData {
  clientCert?: string;
  clientKey?: string;
  caCert?: string;
  url?: string;
}

/**
 * Value that is used in the backend, but never sent over HTTP to the frontend
 */
export interface SecureJsonData {
  apiKey?: string;
}
