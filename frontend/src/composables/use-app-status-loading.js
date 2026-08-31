// 程序状态的hook接口
import { Dialog } from 'quasar';
import SystemApi from 'src/api/SystemApi';
import useInterval from 'src/composables/use-interval';
import { computed, onBeforeUnmount, ref, watch } from 'vue';
import LoadingDialogAppPrepareJobInit from 'components/LoadingDialogAppPrepareError/LoadingDialogAppPrepareJobInit.vue';
import { systemState } from 'src/store/systemState';

let isPreJobLoadingDialogOpened = false;
let preJobLoadingDialog;
const RESTART_OBSERVATION_TIMEOUT_MS = 15000;
const openPreJobLoadingDialog = () => {
  if (isPreJobLoadingDialogOpened) {
    return;
  }
  isPreJobLoadingDialogOpened = true;
  const dialog = Dialog.create({
    component: LoadingDialogAppPrepareJobInit,
  });
  preJobLoadingDialog = dialog;
  dialog.onDismiss(() => {
    if (preJobLoadingDialog === dialog) {
      preJobLoadingDialog = undefined;
      isPreJobLoadingDialogOpened = false;
    }
  });
};

const closePreJobLoadingDialog = () => preJobLoadingDialog?.hide();

// Settings saves restart the backend pre-job pipeline. Keep the actual poller
// owned by App.vue and use this signal instead of creating a second composable
// (and a second interval) at module evaluation time.
export const preJobRefreshRevision = ref(0);
export const requestPreJobStatusRefresh = () => {
  preJobRefreshRevision.value += 1;
};

export const useAppStatusLoading = () => {
  const prepareStatus = computed(() => systemState.preJobStatus);
  const hasPrepareError = computed(
    () =>
      !!prepareStatus.value?.g_error_info ||
      (Array.isArray(prepareStatus.value?.rename_err_results) && prepareStatus.value.rename_err_results.length > 0)
  );
  let loadingRevision = 0;
  let loadingActive = false;
  let waitingForRestart = false;
  let restartObserved = false;
  let restartObservationDeadline = 0;
  let resetInterval = () => {};
  let stopInterval = () => {};

  const stopLoading = () => {
    loadingRevision += 1;
    loadingActive = false;
    waitingForRestart = false;
    restartObserved = false;
    stopInterval();
    closePreJobLoadingDialog();
  };

  const updateDialog = () => {
    if (prepareStatus.value?.is_done !== true || hasPrepareError.value) {
      openPreJobLoadingDialog();
    }
  };

  const getPrepareStatus = async (revision) => {
    const [res, err] = await SystemApi.getPrepareStatus();
    if (!loadingActive || revision !== loadingRevision) return false;
    if (err || !res) {
      if (err?.error?.status === 401 || err?.status === 401) stopLoading();
      else if (waitingForRestart) restartObserved = true;
      return false;
    }
    if (waitingForRestart && res.is_done !== true) restartObserved = true;
    // The settings response is sent just before the backend consumes its
    // restart signal. Ignore the old server's already-done snapshot until a
    // disconnect/incomplete state is observed (or the bounded grace expires).
    if (waitingForRestart && !restartObserved && res.is_done === true && Date.now() < restartObservationDeadline) {
      return true;
    }
    systemState.preJobStatus = res;
    return true;
  };

  ({ resetInterval, stopInterval } = useInterval(
    async () => {
      const revision = loadingRevision;
      if (!(await getPrepareStatus(revision))) return;
      updateDialog();
      if (prepareStatus.value?.is_done === true) {
        if (waitingForRestart && !restartObserved && Date.now() < restartObservationDeadline) return;
        loadingActive = false;
        waitingForRestart = false;
        stopInterval();
        if (!hasPrepareError.value) closePreJobLoadingDialog();
      }
    },
    1000,
    false
  ));

  const startLoading = ({ awaitRestart = false } = {}) => {
    stopInterval();
    loadingRevision += 1;
    loadingActive = true;
    waitingForRestart = awaitRestart;
    restartObserved = false;
    restartObservationDeadline = Date.now() + RESTART_OBSERVATION_TIMEOUT_MS;
    if (awaitRestart) {
      systemState.preJobStatus = {
        is_done: false,
        stage_name: '等待服务重启',
        now_process_info: '正在应用新配置…',
      };
      updateDialog();
    }
    resetInterval();
  };

  watch(
    () => prepareStatus.value?.is_done,
    (val) => {
      if (val === true) {
        if (waitingForRestart && !restartObserved && Date.now() < restartObservationDeadline) return;
        loadingActive = false;
        waitingForRestart = false;
        stopInterval();
        if (hasPrepareError.value) {
          openPreJobLoadingDialog();
        } else {
          closePreJobLoadingDialog();
        }
      }
    }
  );

  onBeforeUnmount(() => {
    stopLoading();
  });

  return {
    startLoading,
    stopLoading,
  };
};
