import { Amplify } from 'aws-amplify'

// Points Amplify Auth at cognito-local instead of real Cognito. Amplify logs
// a "you are using a custom endpoint" warning on configure — expected here.
// authFlowType is forced to USER_PASSWORD_AUTH in auth calls (not SRP) —
// cognito-local doesn't implement SRP (mirrors offline-sync-app's finding).
Amplify.configure({
  Auth: {
    Cognito: {
      userPoolId: import.meta.env.VITE_COGNITO_USER_POOL_ID,
      userPoolClientId: import.meta.env.VITE_COGNITO_CLIENT_ID,
      userPoolEndpoint: import.meta.env.VITE_COGNITO_ENDPOINT,
    },
  },
})
