import { OpenFeature } from '@openfeature/server-sdk';
import { OFREPProvider } from '@openfeature/ofrep-provider';

const endpoint = process.env.OFREP_ENDPOINT ?? 'http://127.0.0.1:8080/environments/production';
const token = process.env.REAPER_EVALUATION_TOKEN;

if (!token) {
  throw new Error('REAPER_EVALUATION_TOKEN is required');
}

await OpenFeature.setProviderAndWait(
  new OFREPProvider({
    baseUrl: endpoint,
    headers: [['Authorization', `Bearer ${token}`]],
  }),
);

const client = OpenFeature.getClient('reaper-typescript-example');
const details = await client.getBooleanDetails('checkout-v2', false, {
  targetingKey: process.env.TARGETING_KEY ?? 'user-2',
  'organization.id': process.env.ORGANIZATION_ID ?? 'acme',
});

console.log(
  JSON.stringify({
    language: 'typescript',
    value: details.value,
    variant: details.variant,
    reason: details.reason,
  }),
);

await OpenFeature.close();
