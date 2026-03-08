import { test, expect } from '@grafana/plugin-e2e';

test('should trigger new query when Resource field is changed', async ({
  panelEditPage,
  readProvisionedDataSource,
  page,
}) => {
  const ds = await readProvisionedDataSource({ fileName: 'datasources.yml', name: 'kubernetes-datasource' });
  await panelEditPage.datasource.set(ds.name);
  const queryReq = panelEditPage.waitForQueryDataRequest();
  const resourceInput = panelEditPage.getQueryEditorRow('A').getByRole('combobox').nth(1);
  await resourceInput.click();
  await resourceInput.fill('Services');
  await page.getByRole('option', { name: 'Services' }).click();
  await expect(await queryReq).toBeTruthy();
});
