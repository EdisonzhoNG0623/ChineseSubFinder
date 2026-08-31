export const requestAccessToken = (config) => {
  const headers = config?.headers;
  const authorization =
    headers?.Authorization ||
    headers?.authorization ||
    (typeof headers?.get === 'function' && headers.get('Authorization'));
  if (typeof authorization !== 'string') return undefined;

  const fields = authorization.trim().split(/\s+/);
  return fields.length === 2 ? fields[1] : undefined;
};
