/** Maps Amplify/Cognito exception names to plain copy. Amplify errors are
 * plain Errors whose `.name` is the Cognito exception name (e.g.
 * "UsernameExistsException") — there is no typed error class to catch on. */
export interface AuthErrorInfo {
  message: string
  /** Present when the error implies a specific next action the UI should offer. */
  action?: 'signIn' | 'resendCode'
}

const MESSAGES: Record<string, AuthErrorInfo> = {
  UsernameExistsException: {
    message: 'An account with this email already exists.',
    action: 'signIn',
  },
  NotAuthorizedException: {
    message: 'Incorrect email or password.',
  },
  UserNotFoundException: {
    message: 'No account found with that email.',
  },
  UserNotConfirmedException: {
    message: 'This account needs to be confirmed first.',
    action: 'resendCode',
  },
  CodeMismatchException: {
    message: 'That confirmation code is incorrect.',
  },
  ExpiredCodeException: {
    message: 'That confirmation code expired. Request a new one.',
    action: 'resendCode',
  },
  InvalidPasswordException: {
    message: 'That password does not meet the requirements.',
  },
  LimitExceededException: {
    message: 'Too many attempts. Please wait a moment and try again.',
  },
}

const FALLBACK: AuthErrorInfo = { message: 'Something went wrong. Please try again.' }

export function describeAuthError(err: unknown): AuthErrorInfo {
  const name = err instanceof Error ? err.name : undefined
  if (name && MESSAGES[name]) return MESSAGES[name]
  return FALLBACK
}
