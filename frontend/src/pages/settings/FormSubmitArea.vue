<template>
  <div class="settings-actions">
    <q-btn :disable="isJobRunning" unelevated label="保存更改" color="primary" type="submit" :loading="submitting" />
    <q-btn :disable="isJobRunning" flat label="撤销未保存更改" @click="resetForm" />
    <q-space />
    <export-setting-btn-dialog />
  </div>
</template>

<script setup>
import { resetForm, submitting } from 'pages/settings/use-settings';
import ExportSettingBtnDialog from 'pages/settings/BtnDialogExportSetting';
import { isJobRunning } from 'src/store/systemState';
</script>
