import { test, expect } from '@grafana/plugin-e2e';

test('should trigger new query when Resource field is changed', async ({
  panelEditPage,
  readProvisionedDataSource,
}) => {
  const ds = await readProvisionedDataSource({ fileName: 'datasources.yml', name: 'kubernetes-datasource' });
  await panelEditPage.datasource.set(ds.name);
  await panelEditPage.getQueryEditorRow('A').getByRole('textbox', { name: 'Action' }).fill('list');
  const queryReq = panelEditPage.waitForQueryDataRequest();
  await panelEditPage.getQueryEditorRow('A').getByRole('textbox', { name: 'Resource' }).fill('pods');
  await expect(await queryReq).toBeTruthy();
});
