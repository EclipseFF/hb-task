let session;

export function signIn() {
  session ??= import('keycloak-js').then(async ({ default: Keycloak }) => {
    const keycloak = new Keycloak({
      url: 'http://localhost:8081', realm: 'vacation', clientId: 'vacation-web',
    });
    await keycloak.init({ onLoad: 'login-required', pkceMethod: 'S256', checkLoginIframe: false });
    return keycloak;
  });
  return session;
}
