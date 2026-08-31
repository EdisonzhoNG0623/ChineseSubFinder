import { reactive } from 'vue';
import { LocalStorage } from 'quasar';

export const userState = reactive({
  username: LocalStorage.getItem('token')?.username,
  accessToken: LocalStorage.getItem('token')?.accessToken,
  // Short-lived read-only resource credentials are deliberately memory-only.
  resourceTicket: undefined,
});
