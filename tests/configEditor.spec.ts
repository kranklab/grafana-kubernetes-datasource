import { test, expect } from '@grafana/plugin-e2e';
import { KubernetesDatasourceOptions, SecureJsonData } from '../src/types';

test('"Save & test" should be successful when configuration is valid', async ({
  createDataSourceConfigPage,
  readProvisionedDataSource,
  page,
}) => {
  const ds = await readProvisionedDataSource<KubernetesDatasourceOptions, SecureJsonData>({
    fileName: 'datasources.yml',
    name: 'kubernetes-datasource',
  });
  const configPage = await createDataSourceConfigPage({ type: ds.type });
  await page.getByRole('textbox', { name: 'Kubernetes URL' }).fill(ds.jsonData.url ?? '');
  await page.getByRole('radio', { name: 'Bearer token' }).click();
  await page.getByRole('textbox', { name: 'Bearer token' }).fill(ds.secureJsonData?.bearerToken ?? '');
  await page.getByRole('textbox', { name: 'CA cert file' }).fill(ds.jsonData.caCert ?? '');
  await configPage.mockHealthCheckResponse({ status: 'OK', message: 'Connected' });
  await expect(configPage.saveAndTest()).toBeOK();
});

test('"Save & test" should fail when configuration is invalid', async ({
  createDataSourceConfigPage,
  readProvisionedDataSource,
  page,
}) => {
  const ds = await readProvisionedDataSource<KubernetesDatasourceOptions, SecureJsonData>({
    fileName: 'datasources.yml',
    name: 'kubernetes-datasource',
  });
  const configPage = await createDataSourceConfigPage({ type: ds.type });
  await page.getByRole('textbox', { name: 'Kubernetes URL' }).fill('https://invalid-host:8443');
  await expect(configPage.saveAndTest()).not.toBeOK();
});
