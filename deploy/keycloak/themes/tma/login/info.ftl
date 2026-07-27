<#import "template.ftl" as layout>
<@layout.registrationLayout displayMessage=false; section>
    <#if section = "header">
        <#if messageHeader??>
            ${kcSanitize(msg("${messageHeader}"))?no_esc}
        <#else>
            ${message.summary}
        </#if>
    <#elseif section = "form">
        <div id="kc-info-message" class="tma-info-message">
            <p class="instruction">${message.summary}<#if requiredActions??><#list requiredActions>: <b><#items as reqActionItem>${kcSanitize(msg("requiredAction.${reqActionItem}"))?no_esc}<#sep>, </#items></b></#list></#if></p>
            <div class="tma-info-actions">
                <#if actionUri?has_content>
                    <a class="pf-v5-c-button pf-m-primary" href="${actionUri}">${kcSanitize(msg("proceedWithAction"))?no_esc}</a>
                </#if>
                <a class="pf-v5-c-button <#if actionUri?has_content>pf-m-secondary<#else>pf-m-primary</#if>" href="${url.loginUrl}">${kcSanitize(msg("backToLogin"))?no_esc}</a>
            </div>
        </div>
    </#if>
</@layout.registrationLayout>
