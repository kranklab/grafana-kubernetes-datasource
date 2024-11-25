import React, { ChangeEvent } from 'react';
import {InlineField, Input, SecretInput} from '@grafana/ui';
import { DataSourcePluginOptionsEditorProps } from '@grafana/data';
import { KubernetesDatasourceOptions, SecureJsonData } from '../types';

interface Props extends DataSourcePluginOptionsEditorProps<KubernetesDatasourceOptions, SecureJsonData> {}

export function ConfigEditor(props: Props) {
  const { onOptionsChange, options } = props;
  const { jsonData, secureJsonFields, secureJsonData } = options;

  const onUrlChange = (event: ChangeEvent<HTMLInputElement>) => {
    onOptionsChange({
      ...options,
      jsonData: {
        ...jsonData,
        url: event.target.value,
      },
    });
  };
  const onClientCertChange = (event: ChangeEvent<HTMLInputElement>) => {
    onOptionsChange({
      ...options,
      jsonData: {
        ...jsonData,
        clientCert: event.target.value,
      },
    });
  };
  const onClientKeyChange = (event: ChangeEvent<HTMLInputElement>) => {
    onOptionsChange({
      ...options,
      jsonData: {
        ...jsonData,
        clientKey: event.target.value,
      },
    });
  };
  const onCaCertChange = (event: ChangeEvent<HTMLInputElement>) => {
    onOptionsChange({
      ...options,
      jsonData: {
        ...jsonData,
        caCert: event.target.value,
      },
    });
  };

  // Secure field (only sent to the backend)
  const onAPIKeyChange = (event: ChangeEvent<HTMLInputElement>) => {
    onOptionsChange({
      ...options,
      secureJsonData: {
        apiKey: event.target.value,
      },
    });
  };

  const onResetAPIKey = () => {
    onOptionsChange({
      ...options,
      secureJsonFields: {
        ...options.secureJsonFields,
        apiKey: false,
      },
      secureJsonData: {
        ...options.secureJsonData,
        apiKey: '',
      },
    });
  };

  return (
    <>
      <InlineField label="Kubernetes URL" labelWidth={20} interactive tooltip={'The url for the kubernetes control plane.'}>
        <Input
          id="config-editor-url"
          onChange={onUrlChange}
          value={jsonData.url}
          placeholder="Enter url for kubernetes control plane."
          width={40}
        />
      </InlineField>
      <InlineField label="cert file" labelWidth={20} interactive tooltip={'The client cert file location.'}>
        <Input
          id="config-editor-url"
          onChange={onClientCertChange}
          value={jsonData.clientCert}
          placeholder="Enter location for kubernetes client cert."
          width={40}
        />
      </InlineField>
      <InlineField label="client key" labelWidth={20} interactive tooltip={'The client key file location'}>
        <Input
          id="config-editor-url"
          onChange={onClientKeyChange}
          value={jsonData.clientKey}
          placeholder="Enter location for kubernetes client key."
          width={40}
        />
      </InlineField>
      <InlineField label="CA cert" labelWidth={20} interactive tooltip={'The CA cert file location'}>
        <Input
          id="config-editor-url"
          onChange={onCaCertChange}
          value={jsonData.caCert}
          placeholder="Enter location for kubernetes CA cert."
          width={40}
        />
      </InlineField>
      <InlineField label="API Key" labelWidth={14} interactive tooltip={'Secure json field (backend only)'}>
        <SecretInput
          required
          id="config-editor-api-key"
          isConfigured={secureJsonFields.apiKey}
          value={secureJsonData?.apiKey}
          placeholder="Enter your API key"
          width={40}
          onReset={onResetAPIKey}
          onChange={onAPIKeyChange}
        />
      </InlineField>
    </>
  );
}
