import React, { ChangeEvent } from 'react';
import { InlineField, Input, RadioButtonGroup, SecretInput, TextArea } from '@grafana/ui';
import { DataSourcePluginOptionsEditorProps } from '@grafana/data';
import { KubernetesDatasourceOptions, SecureJsonData } from '../types';

interface Props extends DataSourcePluginOptionsEditorProps<KubernetesDatasourceOptions, SecureJsonData> {}

const certModeOptions = [
  { label: 'File path', value: 'file' as const },
  { label: 'Inline PEM', value: 'inline' as const },
  { label: 'Bearer token', value: 'token' as const },
  { label: 'EKS / IAM', value: 'aws' as const },
];

const iamModeOptions = [
  { label: 'IAM user', value: 'user' as const },
  { label: 'IAM role', value: 'role' as const },
];

const caCertModeOptions = [
  { label: 'File path', value: 'file' as const },
  { label: 'Inline PEM', value: 'inline' as const },
];

export function ConfigEditor(props: Props) {
  const { onOptionsChange, options } = props;
  const { jsonData, secureJsonFields, secureJsonData } = options;

  const certInputMode = jsonData.certInputMode ?? 'file';

  const onUrlChange = (event: ChangeEvent<HTMLInputElement>) => {
    onOptionsChange({
      ...options,
      jsonData: {
        ...jsonData,
        url: event.target.value,
      },
    });
  };

  const onCertModeChange = (value: 'file' | 'inline' | 'token' | 'aws') => {
    const clearInline = {
      secureJsonFields: {
        ...options.secureJsonFields,
        clientCertData: false,
        clientKeyData: false,
        caCertData: false,
      },
      secureJsonData: {
        ...options.secureJsonData,
        clientCertData: '',
        clientKeyData: '',
        caCertData: '',
      },
    };
    const clearToken = {
      secureJsonFields: { ...options.secureJsonFields, bearerToken: false },
      secureJsonData: { ...options.secureJsonData, bearerToken: '' },
    };
    const clearAws = {
      secureJsonFields: { ...options.secureJsonFields, awsAccessKeyId: false, awsSecretAccessKey: false },
      secureJsonData: { ...options.secureJsonData, awsAccessKeyId: '', awsSecretAccessKey: '' },
    };

    onOptionsChange({
      ...options,
      jsonData: {
        ...jsonData,
        certInputMode: value,
        ...(value !== 'file' ? { clientCert: '', clientKey: '' } : {}),
        ...(value === 'inline' ? { caCert: '' } : {}),
      },
      ...(value === 'file' ? { ...clearInline, ...clearToken, ...clearAws } : {}),
      ...(value === 'inline' ? { ...clearToken, ...clearAws } : {}),
      ...(value === 'token' ? { ...clearInline, ...clearAws } : {}),
      ...(value === 'aws' ? { ...clearInline, ...clearToken } : {}),
    });
  };

  const caCertMode = jsonData.caCertMode ?? 'file';

  const onCaCertModeChange = (value: 'file' | 'inline') => {
    onOptionsChange({
      ...options,
      jsonData: {
        ...jsonData,
        caCertMode: value,
        ...(value === 'inline' ? { caCert: '' } : {}),
      },
      ...(value === 'file'
        ? {
            secureJsonFields: { ...options.secureJsonFields, caCertData: false },
            secureJsonData: { ...options.secureJsonData, caCertData: '' },
          }
        : {}),
    });
  };

  const onClientCertChange = (event: ChangeEvent<HTMLInputElement>) => {
    onOptionsChange({
      ...options,
      jsonData: { ...jsonData, clientCert: event.target.value },
    });
  };

  const onClientKeyChange = (event: ChangeEvent<HTMLInputElement>) => {
    onOptionsChange({
      ...options,
      jsonData: { ...jsonData, clientKey: event.target.value },
    });
  };

  const onCaCertChange = (event: ChangeEvent<HTMLInputElement>) => {
    onOptionsChange({
      ...options,
      jsonData: { ...jsonData, caCert: event.target.value },
    });
  };

  const onClientCertDataChange = (event: ChangeEvent<HTMLTextAreaElement>) => {
    onOptionsChange({
      ...options,
      secureJsonData: { ...options.secureJsonData, clientCertData: event.target.value },
    });
  };

  const onClientKeyDataChange = (event: ChangeEvent<HTMLTextAreaElement>) => {
    onOptionsChange({
      ...options,
      secureJsonData: { ...options.secureJsonData, clientKeyData: event.target.value },
    });
  };

  const onCaCertDataChange = (event: ChangeEvent<HTMLTextAreaElement>) => {
    onOptionsChange({
      ...options,
      secureJsonData: { ...options.secureJsonData, caCertData: event.target.value },
    });
  };

  const onResetClientCertData = () => {
    onOptionsChange({
      ...options,
      secureJsonFields: { ...options.secureJsonFields, clientCertData: false },
      secureJsonData: { ...options.secureJsonData, clientCertData: '' },
    });
  };

  const onResetClientKeyData = () => {
    onOptionsChange({
      ...options,
      secureJsonFields: { ...options.secureJsonFields, clientKeyData: false },
      secureJsonData: { ...options.secureJsonData, clientKeyData: '' },
    });
  };

  const onResetCaCertData = () => {
    onOptionsChange({
      ...options,
      secureJsonFields: { ...options.secureJsonFields, caCertData: false },
      secureJsonData: { ...options.secureJsonData, caCertData: '' },
    });
  };

  const onBearerTokenChange = (event: ChangeEvent<HTMLInputElement>) => {
    onOptionsChange({
      ...options,
      secureJsonData: { ...options.secureJsonData, bearerToken: event.target.value },
    });
  };

  const onResetBearerToken = () => {
    onOptionsChange({
      ...options,
      secureJsonFields: { ...options.secureJsonFields, bearerToken: false },
      secureJsonData: { ...options.secureJsonData, bearerToken: '' },
    });
  };

  const iamMode = jsonData.awsIamMode ?? 'user';

  const onIamModeChange = (value: 'user' | 'role') => {
    onOptionsChange({
      ...options,
      jsonData: { ...jsonData, awsIamMode: value },
      secureJsonFields: { ...options.secureJsonFields, awsAccessKeyId: false, awsSecretAccessKey: false },
      secureJsonData: { ...options.secureJsonData, awsAccessKeyId: '', awsSecretAccessKey: '' },
    });
  };

  const onAwsRegionChange = (event: ChangeEvent<HTMLInputElement>) => {
    onOptionsChange({ ...options, jsonData: { ...jsonData, awsRegion: event.target.value } });
  };

  const onEksClusterNameChange = (event: ChangeEvent<HTMLInputElement>) => {
    onOptionsChange({ ...options, jsonData: { ...jsonData, eksClusterName: event.target.value } });
  };

  const onAwsRoleArnChange = (event: ChangeEvent<HTMLInputElement>) => {
    onOptionsChange({ ...options, jsonData: { ...jsonData, awsRoleArn: event.target.value } });
  };

  const onAwsExternalIdChange = (event: ChangeEvent<HTMLInputElement>) => {
    onOptionsChange({ ...options, jsonData: { ...jsonData, awsExternalId: event.target.value } });
  };

  const onAwsAccessKeyIdChange = (event: ChangeEvent<HTMLInputElement>) => {
    onOptionsChange({ ...options, secureJsonData: { ...options.secureJsonData, awsAccessKeyId: event.target.value } });
  };

  const onAwsSecretAccessKeyChange = (event: ChangeEvent<HTMLInputElement>) => {
    onOptionsChange({ ...options, secureJsonData: { ...options.secureJsonData, awsSecretAccessKey: event.target.value } });
  };

  const onResetAwsAccessKeyId = () => {
    onOptionsChange({
      ...options,
      secureJsonFields: { ...options.secureJsonFields, awsAccessKeyId: false },
      secureJsonData: { ...options.secureJsonData, awsAccessKeyId: '' },
    });
  };

  const onResetAwsSecretAccessKey = () => {
    onOptionsChange({
      ...options,
      secureJsonFields: { ...options.secureJsonFields, awsSecretAccessKey: false },
      secureJsonData: { ...options.secureJsonData, awsSecretAccessKey: '' },
    });
  };

  return (
    <>
      {certInputMode !== 'aws' && (
        <InlineField label="Kubernetes URL" labelWidth={20} interactive tooltip={'The url for the kubernetes control plane.'}>
          <Input
            id="config-editor-url"
            onChange={onUrlChange}
            value={jsonData.url}
            placeholder="Enter url for kubernetes control plane."
            width={40}
          />
        </InlineField>
      )}

      <InlineField label="Auth mode" labelWidth={20} interactive tooltip={'Choose how to authenticate with the Kubernetes API.'}>
        <RadioButtonGroup options={certModeOptions} value={certInputMode} onChange={onCertModeChange} />
      </InlineField>

      {certInputMode === 'file' && (
        <>
          <InlineField label="cert file" labelWidth={20} interactive tooltip={'The client cert file location.'}>
            <Input
              id="config-editor-client-cert"
              onChange={onClientCertChange}
              value={jsonData.clientCert}
              placeholder="Enter location for kubernetes client cert."
              width={40}
            />
          </InlineField>
          <InlineField label="client key" labelWidth={20} interactive tooltip={'The client key file location'}>
            <Input
              id="config-editor-client-key"
              onChange={onClientKeyChange}
              value={jsonData.clientKey}
              placeholder="Enter location for kubernetes client key."
              width={40}
            />
          </InlineField>
          <InlineField label="CA cert" labelWidth={20} interactive tooltip={'The CA cert file location'}>
            <Input
              id="config-editor-ca-cert"
              onChange={onCaCertChange}
              value={jsonData.caCert}
              placeholder="Enter location for kubernetes CA cert."
              width={40}
            />
          </InlineField>
        </>
      )}

      {certInputMode === 'inline' && (
        <>
          <InlineField label="Client cert PEM" labelWidth={20} interactive tooltip={'Paste client certificate PEM content.'}>
            {secureJsonFields.clientCertData ? (
              <SecretInput
                id="config-editor-client-cert-data"
                isConfigured={true}
                value=""
                width={40}
                onReset={onResetClientCertData}
                onChange={() => {}}
              />
            ) : (
              <TextArea
                id="config-editor-client-cert-data"
                onChange={onClientCertDataChange}
                value={secureJsonData?.clientCertData ?? ''}
                placeholder="Paste client certificate PEM content"
                cols={40}
                rows={5}
              />
            )}
          </InlineField>
          <InlineField label="Client key PEM" labelWidth={20} interactive tooltip={'Paste client key PEM content.'}>
            {secureJsonFields.clientKeyData ? (
              <SecretInput
                id="config-editor-client-key-data"
                isConfigured={true}
                value=""
                width={40}
                onReset={onResetClientKeyData}
                onChange={() => {}}
              />
            ) : (
              <TextArea
                id="config-editor-client-key-data"
                onChange={onClientKeyDataChange}
                value={secureJsonData?.clientKeyData ?? ''}
                placeholder="Paste client key PEM content"
                cols={40}
                rows={5}
              />
            )}
          </InlineField>
          <InlineField label="CA cert PEM" labelWidth={20} interactive tooltip={'Paste CA certificate PEM content.'}>
            {secureJsonFields.caCertData ? (
              <SecretInput
                id="config-editor-ca-cert-data"
                isConfigured={true}
                value=""
                width={40}
                onReset={onResetCaCertData}
                onChange={() => {}}
              />
            ) : (
              <TextArea
                id="config-editor-ca-cert-data"
                onChange={onCaCertDataChange}
                value={secureJsonData?.caCertData ?? ''}
                placeholder="Paste CA certificate PEM content"
                cols={40}
                rows={5}
              />
            )}
          </InlineField>
        </>
      )}

      {certInputMode === 'token' && (
        <>
          <InlineField label="Bearer token" labelWidth={20} interactive tooltip={'ServiceAccount token for Kubernetes API authentication.'}>
            <SecretInput
              id="config-editor-bearer-token"
              isConfigured={secureJsonFields.bearerToken ?? false}
              value={secureJsonData?.bearerToken ?? ''}
              placeholder="Paste ServiceAccount bearer token"
              width={40}
              onReset={onResetBearerToken}
              onChange={onBearerTokenChange}
            />
          </InlineField>
          <InlineField label="CA cert input" labelWidth={20} interactive tooltip={'How to supply the CA certificate for verifying the API server.'}>
            <RadioButtonGroup options={caCertModeOptions} value={caCertMode} onChange={onCaCertModeChange} />
          </InlineField>
          {caCertMode === 'file' && (
            <InlineField label="CA cert file" labelWidth={20} interactive tooltip={'Path to the CA certificate file.'}>
              <Input
                id="config-editor-token-ca-cert"
                onChange={onCaCertChange}
                value={jsonData.caCert}
                placeholder="Enter location for kubernetes CA cert."
                width={40}
              />
            </InlineField>
          )}
          {caCertMode === 'inline' && (
            <InlineField label="CA cert PEM" labelWidth={20} interactive tooltip={'Paste CA certificate PEM content.'}>
              {secureJsonFields.caCertData ? (
                <SecretInput
                  id="config-editor-token-ca-cert-data"
                  isConfigured={true}
                  value=""
                  width={40}
                  onReset={onResetCaCertData}
                  onChange={() => {}}
                />
              ) : (
                <TextArea
                  id="config-editor-token-ca-cert-data"
                  onChange={onCaCertDataChange}
                  value={secureJsonData?.caCertData ?? ''}
                  placeholder="Paste CA certificate PEM content"
                  cols={40}
                  rows={5}
                />
              )}
            </InlineField>
          )}
        </>
      )}

      {certInputMode === 'aws' && (
        <>
          <InlineField label="AWS region" labelWidth={20} interactive tooltip={'AWS region where the EKS cluster is deployed.'}>
            <Input
              id="config-editor-aws-region"
              onChange={onAwsRegionChange}
              value={jsonData.awsRegion}
              placeholder="e.g. us-east-1"
              width={40}
            />
          </InlineField>
          <InlineField label="EKS cluster name" labelWidth={20} interactive tooltip={'The name of the EKS cluster.'}>
            <Input
              id="config-editor-eks-cluster"
              onChange={onEksClusterNameChange}
              value={jsonData.eksClusterName}
              placeholder="e.g. my-cluster"
              width={40}
            />
          </InlineField>
          <InlineField label="IAM credential type" labelWidth={20} interactive tooltip={'Use a static IAM user, or assume a role.'}>
            <RadioButtonGroup options={iamModeOptions} value={iamMode} onChange={onIamModeChange} />
          </InlineField>
          {iamMode === 'role' && (
            <>
              <InlineField label="Role ARN" labelWidth={20} interactive tooltip={'ARN of the IAM role to assume.'}>
                <Input
                  id="config-editor-aws-role-arn"
                  onChange={onAwsRoleArnChange}
                  value={jsonData.awsRoleArn}
                  placeholder="arn:aws:iam::123456789012:role/my-role"
                  width={40}
                />
              </InlineField>
              <InlineField label="External ID" labelWidth={20} interactive tooltip={'Optional external ID for cross-account role assumption.'}>
                <Input
                  id="config-editor-aws-external-id"
                  onChange={onAwsExternalIdChange}
                  value={jsonData.awsExternalId}
                  placeholder="Optional"
                  width={40}
                />
              </InlineField>
            </>
          )}
          <InlineField
            label="Access Key ID"
            labelWidth={20}
            interactive
            tooltip={iamMode === 'role' ? 'Optional base credentials for assuming the role. Leave empty to use the default credential chain (env vars, instance profile).' : 'AWS access key ID.'}
          >
            <SecretInput
              id="config-editor-aws-access-key-id"
              isConfigured={secureJsonFields.awsAccessKeyId ?? false}
              value={secureJsonData?.awsAccessKeyId ?? ''}
              placeholder={iamMode === 'role' ? 'Optional' : 'AKIAIOSFODNN7EXAMPLE'}
              width={40}
              onReset={onResetAwsAccessKeyId}
              onChange={onAwsAccessKeyIdChange}
            />
          </InlineField>
          <InlineField
            label="Secret Access Key"
            labelWidth={20}
            interactive
            tooltip={iamMode === 'role' ? 'Optional. Required if Access Key ID is set.' : 'AWS secret access key.'}
          >
            <SecretInput
              id="config-editor-aws-secret-key"
              isConfigured={secureJsonFields.awsSecretAccessKey ?? false}
              value={secureJsonData?.awsSecretAccessKey ?? ''}
              placeholder={iamMode === 'role' ? 'Optional' : ''}
              width={40}
              onReset={onResetAwsSecretAccessKey}
              onChange={onAwsSecretAccessKeyChange}
            />
          </InlineField>
        </>
      )}

    </>
  );
}
