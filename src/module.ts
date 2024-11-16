import { DataSourcePlugin } from '@grafana/data';
import { DataSource } from './datasource';
import { ConfigEditor } from './components/ConfigEditor';
import { QueryEditor } from './components/QueryEditor';
import { KubernetesQuery, KubernetesDatasourceOptions } from './types';

export const plugin = new DataSourcePlugin<DataSource, KubernetesQuery, KubernetesDatasourceOptions>(DataSource)
  .setConfigEditor(ConfigEditor)
  .setQueryEditor(QueryEditor);
