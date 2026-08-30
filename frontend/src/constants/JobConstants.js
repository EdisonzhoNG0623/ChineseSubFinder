export const JOB_STATUS_PENDING = 0;
export const JOB_STATUS_IN_PROGRESS = 1;
export const JOB_STATUS_FAILED = 2;
export const JOB_STATUS_COMPLETED = 3;
export const JOB_STATUS_DOWNLOADING = 4;
export const JOB_STATUS_IGNORE = 5;

export const JOB_STATUS_MAP = {
  [JOB_STATUS_PENDING]: '等待下载',
  [JOB_STATUS_IN_PROGRESS]: '处理中',
  [JOB_STATUS_FAILED]: '失败',
  [JOB_STATUS_COMPLETED]: '已完成',
  [JOB_STATUS_DOWNLOADING]: '下载中',
  [JOB_STATUS_IGNORE]: '忽略',
};

export const JOB_STATUS_COLOR_MAP = {
  [JOB_STATUS_PENDING]: '#bbb',
  [JOB_STATUS_IN_PROGRESS]: '#3874CB',
  [JOB_STATUS_COMPLETED]: '#59B755',
  [JOB_STATUS_FAILED]: '#EB5451',
  [JOB_STATUS_DOWNLOADING]: '#3874CB',
  [JOB_STATUS_IGNORE]: '#F5DA5F',
};

export const JOB_STATUS_OPTIONS = Object.keys(JOB_STATUS_MAP).map((k) => ({
  label: JOB_STATUS_MAP[k],
  value: parseInt(k, 10),
}));

export const ERROR_CATEGORY_META = {
  NONE: { label: '无错误', color: 'grey-6' },
  NO_SUBTITLE: { label: '未命中字幕', color: 'warning' },
  TRANSIENT: { label: '网络/临时错误', color: 'orange-8' },
  PROVIDER_UNAVAILABLE: { label: '字幕源暂不可用', color: 'orange-9' },
  QUOTA: { label: '字幕源配额等待', color: 'amber-9' },
  LOCAL: { label: '本地文件错误', color: 'negative' },
  PROVIDER_BLOCKED: { label: '字幕源验证/封禁', color: 'deep-orange' },
  UNKNOWN: { label: '未知错误', color: 'grey-8' },
};
