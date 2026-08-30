import { onBeforeUnmount, ref } from 'vue';

const useInterval = (fn, ms, autoStart = true, options = {}) => {
  const timer = ref(null);
  const running = ref(false);
  const enabled = ref(autoStart);
  const { pauseWhenHidden = true, runOnVisible = true } = options;
  const hasDocument = typeof document !== 'undefined';
  let pending = false;
  let disposed = false;
  const isHidden = () => pauseWhenHidden && hasDocument && document.hidden;

  const clearTimer = () => {
    if (timer.value !== null) clearInterval(timer.value);
    timer.value = null;
  };
  let invoke;
  const finishInvoke = () => {
    running.value = false;
    if (!disposed && pending && enabled.value && !isHidden()) {
      pending = false;
      invoke();
    }
  };
  invoke = () => {
    if (disposed || isHidden()) return;
    if (running.value) {
      pending = true;
      return;
    }
    running.value = true;
    let result;
    try {
      result = fn();
    } catch (error) {
      finishInvoke();
      throw error;
    }
    Promise.resolve(result).then(finishInvoke, (error) => {
      finishInvoke();
      setTimeout(() => {
        throw error;
      }, 0);
    });
  };
  const schedule = () => {
    clearTimer();
    if (!enabled.value || isHidden()) return;
    timer.value = setInterval(invoke, ms);
  };
  const resetInterval = () => {
    enabled.value = true;
    schedule();
    invoke();
  };
  const stopInterval = () => {
    enabled.value = false;
    pending = false;
    clearTimer();
  };

  const handleVisibilityChange = () => {
    if (!enabled.value) return;
    if (isHidden()) {
      clearTimer();
      return;
    }
    schedule();
    if (runOnVisible) invoke();
  };

  if (hasDocument) document.addEventListener('visibilitychange', handleVisibilityChange);
  if (autoStart) {
    schedule();
    invoke();
  }
  onBeforeUnmount(() => {
    disposed = true;
    pending = false;
    clearTimer();
    if (hasDocument) document.removeEventListener('visibilitychange', handleVisibilityChange);
  });
  return {
    timer,
    running,
    resetInterval,
    stopInterval,
  };
};

export default useInterval;
