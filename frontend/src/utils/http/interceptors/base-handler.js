import { router } from 'src/router';
import { LocalStorage } from 'quasar';
import { userState } from 'src/store/userState';
import { requestAccessToken } from 'src/utils/http/authorization';

const clearAuthState = () => {
  userState.username = '';
  userState.accessToken = undefined;
  userState.resourceTicket = undefined;
  LocalStorage.remove('token');
};

const handleError = (error) => {
  // eslint-disable-next-line
  console.error('request failed', error?.status || '', error?.data?.message || error?.message || 'network error');
  let errorMessageText = error?.data?.message || error?.message || '网络错误';
  // 权限不足时的处理
  if (error.status === 401) {
    errorMessageText = error.data?.message || '权限不足，请登录重试';
    const currentToken = userState.accessToken;
    const rejectedToken = requestAccessToken(error.config);
    // A late 401 from an older session must not log out a newly authenticated
    // session. With no active session, routing to login remains appropriate.
    if (!currentToken || (rejectedToken && rejectedToken === currentToken)) {
      clearAuthState();
      router.push('/access/login');
    }
  }

  const rtData = {
    error,
    message: errorMessageText,
  };

  return Promise.reject(rtData);
};

export default {
  onRequestRejected: (error) => handleError(error),
  onResponseFullFilled: (response) => {
    const { data } = response;
    // 正常返回但是code是错误码的情况也需要异常处理
    if (data?.code && data?.code > 300) {
      return handleError(response);
    }
    return response;
  },
  onResponseRejected: (error) => handleError(error?.response || error),
};
