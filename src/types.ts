import { DataSourceJsonData } from '@grafana/data';
import { DataQuery } from '@grafana/schema';

export interface KubernetesQuery extends DataQuery {
  action: string | 'get' | 'list' | 'summary';
  namespace: string;
  resource: string;
  name?: string;
}

export const DEFAULT_QUERY: Partial<KubernetesQuery> = {
  action: 'list',
  namespace: 'default',
  resource: 'pods',
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
  certInputMode?: 'file' | 'inline' | 'token' | 'aws';
  caCertMode?: 'file' | 'inline';
  clientCert?: string;
  clientKey?: string;
  caCert?: string;
  url?: string;
  awsRegion?: string;
  eksClusterName?: string;
  awsIamMode?: 'user' | 'role';
  awsRoleArn?: string;
  awsExternalId?: string;
}

/**
 * Value that is used in the backend, but never sent over HTTP to the frontend
 */
export interface SecureJsonData {
  clientCertData?: string;
  clientKeyData?: string;
  caCertData?: string;
  bearerToken?: string;
  awsAccessKeyId?: string;
  awsSecretAccessKey?: string;
}
