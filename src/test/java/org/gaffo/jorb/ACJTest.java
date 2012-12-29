package org.gaffo.jorb;

import org.gaffo.jorb.acjtest.*;
import org.junit.Test;

import java.util.ArrayList;
import java.util.List;

import static org.junit.Assert.assertTrue;
import static org.junit.Assert.fail;
import static org.mockito.Matchers.any;
import static org.mockito.Mockito.*;

public class ACJTest
{

    List<Object> verifies = new ArrayList<Object>();

    @Test
    public void process_findsSubclassMethodWithParamsFromContext() throws Exception
    {
        IContext mContext = mock(IContext.class);

        I1 mi1 = mock(I1.class);
        when(mContext.get(I1.class)).thenReturn(mi1);

        new Job1(this).process(mContext);

        assertTrue(verifies.contains(mi1));
    }

    @Test
    public void process_withMutlipleParameters_works() throws Exception
    {
        IContext mContext = mock(IContext.class);

        I1 mi1 = mock(I1.class);
        when(mContext.get(I1.class)).thenReturn(mi1);

        I2 mi2 = mock(I2.class);
        when(mContext.get(I2.class)).thenReturn(mi2);

        when(mContext.get(String.class)).thenReturn("HI");

        new Job2(this).process(mContext);

        assertTrue(verifies.contains(mi1));
        assertTrue(verifies.contains(mi2));
        assertTrue(verifies.contains("HI"));
    }

    @Test
    public void process_throwingException_sendsExceptionToJobExceptionHandler()
    {
        IContext mContext = mock(IContext.class);

        I1 mi1 = mock(I1.class);
        when(mContext.get(I1.class)).thenReturn(mi1);

        IJobErrorHandler errHandler = mock(IJobErrorHandler.class);
        when(mContext.get(IJobErrorHandler.class)).thenReturn(errHandler);

        Job1 job = mock(Job1.class);
        Exception e = new RuntimeException("E");
        doThrow(e).when(job).process(any(I1.class));

        job.process(mContext);

        verify(errHandler).handleException(e, job);
    }

    @Test
    public void process_noMatchingMethod()
    {
        IContext mContext = mock(IContext.class);
        try{
            new NotJob().process(mContext);
            fail("Should have thrown " + ACJ.NoProcessMethodException.class.getName());
        }
        catch (ACJ.NoProcessMethodException e)
        {
        }
    }

    @Test
    public void process_contextMissingParameters()
    {
        IContext mContext = mock(IContext.class);
        try
        {
            new Job2(this).process(mContext);
            fail("Should have thrown " + ACJ.RequiredParametersMissingFromContextException.class.getName());
        }
        catch (ACJ.RequiredParametersMissingFromContextException e)
        {
            assertTrue(e.missingParameterTypes().contains(I1.class));
            assertTrue(e.missingParameterTypes().contains(I2.class));
            assertTrue(e.missingParameterTypes().contains(String.class));
        }
    }

    @Test
    public void process_multipleMatches()
    {
        IContext mContext = mock(IContext.class);
        try
        {
            new MultiProcess(this).process(mContext);
            fail("Should have thrown " + ACJ.MultipleMatchingProcessMethodsException.class.getName());
        }
        catch (ACJ.MultipleMatchingProcessMethodsException e)
        {
        }
    }

    public void verifyCallback(Object ... args)
    {
        for (Object o : args)
        {
            verifies.add(o);
        }
    }
}
