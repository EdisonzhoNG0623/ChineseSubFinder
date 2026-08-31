import { userState } from 'src/store/userState';
import { requestAccessToken } from 'src/utils/http/authorization';

const RESOURCE_TICKET_HEADER = 'x-csf-resource-ticket';
const REFRESH_BEFORE_EXPIRY_MS = 2 * 60 * 1000;

const expiresAt = (ticket) => {
  const seconds = Number.parseInt(ticket?.split('.', 1)[0], 10);
  return Number.isFinite(seconds) ? seconds * 1000 : 0;
};

export default {
  onResponseFullFilled: (response) => {
    const nextTicket = response.headers?.[RESOURCE_TICKET_HEADER];
    if (!nextTicket) return response;

    const currentAccessToken = userState.accessToken;
    if (!currentAccessToken || requestAccessToken(response.config) !== currentAccessToken) return response;

    const currentTicket = userState.resourceTicket;
    if (!currentTicket || expiresAt(currentTicket) - Date.now() <= REFRESH_BEFORE_EXPIRY_MS) {
      userState.resourceTicket = nextTicket;
    }
    return response;
  },
};
