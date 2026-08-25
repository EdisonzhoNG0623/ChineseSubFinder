<template>
  <div class="settings-panel">
    <q-list dense class="settings-form-list">
      <q-item tag="label" v-ripple>
        <q-item-section>
          <q-item-label>使用出站代理</q-item-label>
          <q-item-label caption>供 TMDB、字幕站和浏览器任务访问外部网络，支持 HTTP 与 SOCKS5</q-item-label>
        </q-item-section>
        <q-item-section avatar top>
          <q-toggle v-model="form.proxy_settings.use_proxy" />
        </q-item-section>
      </q-item>

      <q-item v-if="form.proxy_settings.use_proxy" class="q-mt-md" dense>
        <q-item-section>
          <div class="row q-gutter-sm">
            <q-select
              v-model="form.proxy_settings.use_which_proxy_protocol"
              :options="Object.keys(PROXY_TYPE_NAME_MAP).map((e) => ({ label: PROXY_TYPE_NAME_MAP[e], value: e }))"
              label="协议"
              standout
              dense
              emit-value
              map-options
              style="width: 100px"
            />
            <q-input v-model="form.proxy_settings.input_proxy_address" standout dense label="代理服务器" />
            <q-input v-model="form.proxy_settings.input_proxy_port" standout dense label="代理端口" />
            <q-input v-model="form.proxy_settings.local_http_proxy_server_port" standout dense label="本地端口" />
          </div>

          <div class="q-mt-sm row q-gutter-sm">
            <q-checkbox v-model="form.proxy_settings.need_pwd" left-label label="账号认证" />
            <q-input
              :disable="!form.proxy_settings.need_pwd"
              v-model="form.proxy_settings.input_proxy_username"
              standout
              dense
              label="账号"
            />
            <q-input
              :disable="!form.proxy_settings.need_pwd"
              v-model="form.proxy_settings.input_proxy_password"
              type="password"
              autocomplete="new-password"
              standout
              dense
              label="密码"
            />
          </div>

          <div class="q-mt-sm">
            <proxy-check-btn
              :settings="form.proxy_settings"
              label="测试代理服务"
              size="md"
              icon="bolt"
              color="primary"
            />
          </div>
        </q-item-section>
      </q-item>

      <q-separator spaced inset></q-separator>

      <q-item tag="label" v-ripple>
        <q-item-section>
          <q-item-label>调试模式</q-item-label>
          <q-item-label caption>输出更详细的诊断日志；日常运行建议关闭</q-item-label>
        </q-item-section>
        <q-item-section avatar>
          <q-toggle v-model="form.debug_mode" />
        </q-item-section>
      </q-item>

      <q-separator spaced inset></q-separator>

      <q-item tag="label" v-ripple>
        <q-item-section>
          <q-item-label>保存整季的缓存字幕</q-item-label>
          <q-item-label caption>保留季包解压后的临时字幕，便于排查匹配问题但会占用更多空间</q-item-label>
        </q-item-section>
        <q-item-section avatar>
          <q-toggle v-model="form.save_full_season_tmp_subtitles" />
        </q-item-section>
      </q-item>

      <q-separator spaced inset></q-separator>

      <q-item>
        <q-item-section>
          <q-item-label>字幕格式下载优先级</q-item-label>
        </q-item-section>
        <q-item-section avatar>
          <div class="row">
            <q-radio
              v-for="(v, k) in SUB_TYPE_PRIORITY_NAME_MAP"
              :key="k"
              v-model="form.sub_type_priority"
              :val="~~k"
              :label="v"
            />
          </div>
        </q-item-section>
      </q-item>

      <q-separator spaced inset></q-separator>

      <q-item>
        <q-item-section>
          <q-item-label>字幕保存的命名格式</q-item-label>
          <q-item v-for="(v, k) in SUB_NAME_FORMAT_NAME_MAP" :key="k" tag="label" v-ripple>
            <q-item-section avatar top>
              <q-radio v-model="form.sub_name_formatter" :val="~~k" />
            </q-item-section>
            <q-item-section>
              <q-item-label>{{ v }}</q-item-label>
              <q-item-label caption>
                {{ subNameFormatDescMap[k] }}
              </q-item-label>
            </q-item-section>
          </q-item>
          <div class="status-strip status-strip--warning q-mt-md">
            修改命名格式后需要重启容器；启动阶段会整理已有字幕，媒体库较大时可能耗时较长。
          </div>
        </q-item-section>
      </q-item>

      <q-separator spaced inset></q-separator>

      <q-item tag="label" v-ripple>
        <q-item-section>
          <q-item-label>跳过中文电影</q-item-label>
          <q-item-label caption>识别为中文原声的电影不再自动下载字幕</q-item-label>
        </q-item-section>
        <q-item-section avatar>
          <q-toggle v-model="form.scan_logic.skip_chinese_movie" />
        </q-item-section>
      </q-item>

      <q-item tag="label" v-ripple>
        <q-item-section>
          <q-item-label>跳过中文连续剧</q-item-label>
          <q-item-label caption>识别为中文原声的连续剧不再自动下载字幕</q-item-label>
        </q-item-section>
        <q-item-section avatar>
          <q-toggle v-model="form.scan_logic.skip_chinese_series" />
        </q-item-section>
      </q-item>

      <q-item v-if="SUB_NAME_FORMAT_EMBY === form.sub_name_formatter" tag="label" v-ripple>
        <q-item-section>
          <q-item-label>保存多字幕</q-item-label>
          <q-item-label caption>保留每个字幕源的最佳结果；仅适用于 Emby 命名格式</q-item-label>
        </q-item-section>
        <q-item-section avatar>
          <q-toggle v-model="form.save_multi_sub" />
        </q-item-section>
      </q-item>

      <q-separator spaced inset />

      <q-item>
        <q-item-section>
          <q-item-label class="section-title q-mb-sm">队列与重试</q-item-label>
          <q-input
            class="col"
            v-model.number="form.task_queue.max_retry_times"
            label="最大重试次数"
            shadow-text="单个任务失败后，最大重试次数，超过后会降一级"
            standout
            dense
            :rules="[(val) => !!val || '不能为空']"
          />
          <q-input
            class="col"
            v-model.number="form.task_queue.one_job_time_out"
            label="单个任务总超时"
            shadow-text="包含字幕源搜索、候选处理与保存；超时后按重试策略处理"
            standout
            dense
            suffix="秒"
            :rules="[(val) => !!val || '不能为空']"
          />
          <q-input
            class="col"
            v-model.number="form.task_queue.download_concurrency"
            type="number"
            min="1"
            max="4"
            label="并行下载任务数"
            shadow-text="建议 2；机械硬盘或慢速网络盘使用 1，高性能本地存储可尝试 3–4"
            standout
            dense
            suffix="个"
            :rules="[(val) => (val >= 1 && val <= 4) || '请输入 1–4']"
          />
          <q-input
            class="col"
            v-model.number="form.task_queue.interval"
            label="队列轮询间隔"
            shadow-text="每隔多久尝试领取新任务；实际并发仍受上方并行数限制"
            standout
            dense
            suffix="秒"
            :rules="[(val) => !!val || '不能为空']"
          />
          <div class="status-strip status-strip--warning q-mt-sm">
            并行下载任务数和队列轮询间隔在服务启动时载入，修改后需重启容器。
          </div>
          <q-input
            class="col"
            v-model.number="form.task_queue.expiration_time"
            label="媒体下载时效"
            shadow-text="只为创建时间仍在范围内的媒体自动下载字幕"
            standout
            dense
            suffix="天"
            :rules="[(val) => !!val || '不能为空']"
          />
          <q-input
            class="col"
            v-model.number="form.task_queue.download_sub_during_x_days"
            label="已有内置中文字幕时效"
            shadow-text="超过该天数且已有内置中文字幕时跳过下载"
            standout
            dense
            suffix="天"
            :rules="[(val) => !!val || '不能为空']"
          />
          <q-input
            class="col"
            v-model.number="form.task_queue.one_sub_download_interval"
            label="失败后的最小重试间隔"
            standout
            dense
            suffix="小时"
            :rules="[(val) => !!val || '不能为空']"
          />
          <q-input
            class="col"
            v-model="form.task_queue.check_pulic_ip_target_site"
            label="公网 IP 检测地址"
            shadow-text="目标需直接返回 IP 文本；多个地址用英文分号分隔"
            standout
            dense
          />
          <div class="text-caption text-grey-7">留空使用内置检测地址；只有内置服务不可用时才需要覆盖。</div>
        </q-item-section>
      </q-item>

      <q-separator spaced inset />

      <q-item>
        <q-item-section>
          <q-item-label>下载缓存保留时间</q-item-label>
        </q-item-section>
        <q-item-section avatar>
          <div class="row no-wrap q-gutter-xs">
            <q-input class="col" standout dense v-model.number="form.download_file_cache.ttl"> </q-input>
            <q-select
              standout
              dense
              :options="[
                { label: '小时', value: 'hour' },
                { label: '秒', value: 'second' },
              ]"
              emit-value
              map-options
              v-model.number="form.download_file_cache.unit"
            ></q-select>
          </div>
        </q-item-section>
      </q-item>

      <q-separator spaced inset />

      <q-item>
        <q-item-section class="items-start" top>
          <q-item-label>自定义视频扩展名</q-item-label>
          <q-item-label caption>在默认支持的 mp4、mkv、rmvb、iso 之外追加扩展名</q-item-label>
          <template v-for="(item, i) in form.custom_video_exts" :key="i">
            <div class="row items-center q-gutter-x-md" :class="{ 'q-mt-md': i === 0 }">
              <q-input
                v-model="form.custom_video_exts[i]"
                placeholder=""
                standout
                dense
                :rules="[(val) => !!val || '不能为空']"
              />
              <q-btn
                icon="remove"
                color="negative"
                dense
                rounded
                size="xs"
                title="删除"
                @click="form.custom_video_exts.splice(i, 1)"
              ></q-btn>
            </div>
          </template>
        </q-item-section>
        <q-item-section side top>
          <q-btn
            icon="add"
            color="primary"
            dense
            rounded
            size="xs"
            title="新增"
            @click="form.custom_video_exts.push('')"
          ></q-btn>
        </q-item-section>
      </q-item>

      <q-separator spaced inset></q-separator>

      <q-item tag="label" v-ripple>
        <q-item-section>
          <q-item-label>自动校正字幕时间轴</q-item-label>
          <q-item-label caption>需要额外分析视频；网络盘或低性能存储建议谨慎开启</q-item-label>
        </q-item-section>
        <q-item-section avatar>
          <q-toggle v-model="form.fix_time_line" />
        </q-item-section>
      </q-item>

      <q-separator spaced inset></q-separator>

      <q-item tag="label" v-ripple>
        <q-item-section>
          <q-item-label>使用自定义 TMDB API</q-item-label>
          <q-item-label caption>用于媒体身份识别；公共查询不稳定时可配置自己的 v3 API Key</q-item-label>
        </q-item-section>
        <q-item-section avatar top>
          <q-toggle v-model="form.tmdb_api_settings.enable" />
        </q-item-section>
      </q-item>

      <template v-if="form.tmdb_api_settings.enable">
        <q-item>
          <div class="text-warning">当前只兼容 TMDB v3 API Key；若网络无法直连，请同时配置出站代理或备用地址。</div>
        </q-item>
        <q-item class="q-mt-md" dense>
          <q-item-section>
            <q-input
              v-model="form.tmdb_api_settings.api_key"
              type="password"
              autocomplete="new-password"
              standout
              dense
              label="TMDB v3 API Key"
              :rules="[(val) => (form.tmdb_api_settings.enable && !!val) || '不能为空']"
            />
          </q-item-section>
        </q-item>
        <q-item dense>
          <q-item-section>
            <q-checkbox
              v-model="form.tmdb_api_settings.use_alternate_base_url"
              label="使用备用的 TMDB API 地址"
              title="如果连接不上 TMDB API，可以尝试勾选这个选项"
            />
          </q-item-section>
        </q-item>
        <q-item>
          <btn-check-tmdb-api />
        </q-item>
      </template>
    </q-list>
  </div>
</template>

<script setup>
import {
  SUB_NAME_FORMAT_EMBY,
  SUB_NAME_FORMAT_NORMAL,
  SUB_NAME_FORMAT_NAME_MAP,
  SUB_TYPE_PRIORITY_NAME_MAP,
  PROXY_TYPE_NAME_MAP,
  SUB_NAME_VIDEO,
} from 'src/constants/SettingConstants';
import { formModel } from 'pages/settings/use-settings';
import { toRefs } from '@vueuse/core';
import ProxyCheckBtn from 'components/ProxyCheckBtn';
import BtnCheckTmdbApi from 'pages/settings/BtnCheckTmdbApi';

const subNameFormatDescMap = {
  [SUB_NAME_FORMAT_NORMAL]: '兼容性更好，AAA.zh.ass or AAA.zh.default.ass。',
  [SUB_NAME_FORMAT_EMBY]: 'AAA.chinese(简英,subhd).ass or AAA.chinese(简英,xunlei).default.ass。',
  [SUB_NAME_VIDEO]: '无语言描述后缀，AAA.ass or AAA.srt',
};

const { advanced_settings: form } = toRefs(formModel);
</script>
